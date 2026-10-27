package chord

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// Ports packages/chord/src/watch.ts (Watch, NativeWatch, NativeWatchHandle and their shared lifecycle).

// maxPendingNativeValues is the maximum number of undelivered values retained behind one unavailable native watch listener.
const maxPendingNativeValues = 100

// NativeWatchHandle is a serialized observation of an in-process value: as WatchHandle, but a change is a new value
// without ops.
type NativeWatchHandle[T any] interface {
	// Value is the acquisition value before Start and the latest delivered value afterward.
	Value() T
	// Start installs the sole asynchronous listener. It never invokes it inline.
	Start(listener func(ctx context.Context, change NativeWatchChange[T]) error)
	// Stop idempotently stops future callbacks and returns this watch's terminal result.
	Stop() (WatchEnd, error)
	// Closed returns a channel closed when the watch terminates; an already-running callback remains caller-owned.
	Closed() <-chan struct{}
	// End returns the terminal result once Closed is closed.
	End() WatchEnd
}

// pending counts deliveries upstream runs from the microtask queue, so a caller can wait until every scheduled delivery
// has drained.
type pending struct {
	mu         sync.Mutex
	count      int
	idle       *sync.Cond
	deliveries []*delivery
	// holds counts the open Hold calls; while any is open, deliveries watches would start wait in held.
	holds int
	held  []func()
}

// delivery is one scheduled delivery goroutine. started is closed once id is set; done once the goroutine ended.
type delivery struct {
	id      uint64
	started chan struct{}
	done    chan struct{}
}

func (d *delivery) finished() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}

func newPending() *pending {
	tracker := &pending{}
	tracker.idle = sync.NewCond(&tracker.mu)
	return tracker
}

func (tracker *pending) begin() {
	tracker.mu.Lock()
	tracker.count++
	tracker.mu.Unlock()
}

func (tracker *pending) end() {
	tracker.mu.Lock()
	tracker.count--
	if tracker.count == 0 {
		tracker.idle.Broadcast()
	}
	tracker.mu.Unlock()
}

// wait blocks until no scheduled delivery is queued or running.
func (tracker *pending) wait() {
	tracker.mu.Lock()
	for tracker.count != 0 {
		tracker.idle.Wait()
	}
	tracker.mu.Unlock()
}

// hold makes the deliveries advanced watches would start wait until the matching release; it returns release.
func (tracker *pending) hold() (release func()) {
	tracker.mu.Lock()
	tracker.holds++
	tracker.mu.Unlock()
	return func() {
		tracker.mu.Lock()
		tracker.holds--
		var held []func()
		if tracker.holds == 0 {
			held, tracker.held = tracker.held, nil
		}
		tracker.mu.Unlock()
		for _, start := range held {
			start()
		}
	}
}

// holding queues start to run at the release of the last open hold and reports true, or reports false when no hold is
// open.
func (tracker *pending) holding(start func()) bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.holds == 0 {
		return false
	}
	tracker.held = append(tracker.held, start)
	return true
}

// schedule runs job off the caller's goroutine, as upstream's queueMicrotask runs it outside the publishing call.
// awaitEntered waits for what it scheduled.
func (tracker *pending) schedule(job func()) {
	d := &delivery{started: make(chan struct{}), done: make(chan struct{})}
	tracker.begin()
	tracker.mu.Lock()
	tracker.deliveries = append(slices.DeleteFunc(tracker.deliveries, (*delivery).finished), d)
	tracker.mu.Unlock()
	//portlint:allow asyncorder joined by pending.wait (begin/end count); watch frames are taken under watch.mu in publish order and one runs at a time, as watch.ts #schedule/#drain
	go func() {
		d.id = goroutineid.Current()
		close(d.started)
		defer close(d.done)
		defer tracker.end()
		job()
	}()
}

// awaitEntered blocks until every delivery scheduled before the call has entered: its callback returned or is parked in a
// wait or blocked behind a lock (internal/entered.AwaitBlocked), never until it finishes a wait of its own. Upstream's
// drain microtask enters the listener, up to the listener's first await, before the code that awaited a commit's promise
// resumes; a Go caller that resumes on another goroutine awaits the deliveries it would otherwise outrun. A Go listener
// that waits for a lock is the analogue of an upstream listener's await, and the caller may be the holder: it is not
// waited for. A delivery on the caller's own goroutine is skipped, and a listener that waits for the Harness is parked
// in that wait, so waiting listeners never wait for each other. It returns the cause of ctx when ctx ends the wait.
func (tracker *pending) awaitEntered(ctx context.Context) error {
	tracker.mu.Lock()
	scheduled := slices.DeleteFunc(slices.Clone(tracker.deliveries), (*delivery).finished)
	tracker.mu.Unlock()
	if len(scheduled) == 0 {
		return nil
	}
	self := goroutineid.Current()
	for _, d := range scheduled {
		select {
		case <-d.started:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
		if d.id == self {
			continue
		}
		if err := entered.AwaitBlocked(ctx, d.id, d.done); err != nil {
			return err
		}
	}
	return nil
}

// DeliveryScheduler runs the deliveries of the watches built on it, each queued as upstream's microtask is. A host owns
// one: the Durable Session for its watches, the native Durable core driver for its own.
type DeliveryScheduler struct{ pending *pending }

// defaultDeliveries runs the deliveries of a watch that no host moved to its own scheduler (Watch.On, NativeWatch.On). It is built
// when the first such watch is, not by an initializer on every start.
func defaultDeliveries() *DeliveryScheduler {
	defaultDeliveriesOnce.Do(func() { defaultDeliveriesScheduler = NewDeliveryScheduler() })
	return defaultDeliveriesScheduler
}

var (
	defaultDeliveriesOnce      sync.Once
	defaultDeliveriesScheduler *DeliveryScheduler
)

// NewDeliveryScheduler returns an idle delivery scheduler.
func NewDeliveryScheduler() *DeliveryScheduler { return &DeliveryScheduler{pending: newPending()} }

// Wait blocks until no scheduled delivery is queued or running.
func (s *DeliveryScheduler) Wait() { s.pending.wait() }

// Hold makes the deliveries that frames advanced from now on would start wait until the returned release runs, which
// starts them in the order the watches were first advanced. Frames a watch is advanced with meanwhile fold into one call,
// as the frames of one publication fold into the single microtask upstream queues for it. Holds nest; release is called
// once.
func (s *DeliveryScheduler) Hold() (release func()) { return s.pending.hold() }

// AwaitEntered blocks until every delivery scheduled before the call has entered its listener, up to the listener's
// first wait; it returns the cause of ctx when ctx ends the wait.
func (s *DeliveryScheduler) AwaitEntered(ctx context.Context) error {
	return s.pending.awaitEntered(ctx)
}

// watchFrame is one listener call: the value it delivers, the ops and retirement flag of a JSON watch, and the context of
// the newest frame it covers. The listener gets the change and the context apart: a change is plain data.
type watchFrame[T any] struct {
	value   T
	ops     []delta.Op
	ctx     context.Context
	retired bool
}

// watchLifecycle is the lifecycle both handles share: serialized delivery and termination. A source feeds it and ends
// it explicitly; termination drops every reference the watch holds into its source, so a stopped watch that is still
// referenced keeps nothing but its last value alive. The handles keep what is pending (next, hasPending, release) and
// say how a listener is called (invoke).
type watchLifecycle[T, L any] struct {
	mu        sync.Mutex
	scheduler *pending
	detach    func()
	// report receives a listener's failure, which also ends the watch; nil drops it.
	report func(error)
	invoke func(listener L, frame watchFrame[T]) error
	// next takes the next delivery, under mu; false when nothing is pending.
	next func() (watchFrame[T], bool)
	// hasPending reports whether a delivery is pending, under mu.
	hasPending func() bool
	// release drops everything pending and every reference into the source, under mu; called once, on termination.
	release   func()
	closed    chan struct{}
	value     T
	listener  L
	listening bool
	started   bool
	scheduled bool
	running   bool
	// holdQueued is set while a delivery waits for the release of a Hold.
	holdQueued bool
	// ending is set by EndAfterDelivery: the source sends nothing more, and the watch ends ended once it delivered the rest.
	ending    bool
	detached  bool
	end       *WatchEnd
	resolved  bool
	stopAfter func() bool
}

func (watch *watchLifecycle[T, L]) setup(value T, detach func(), report func(error)) {
	watch.scheduler = defaultDeliveries().pending
	watch.detach = detach
	watch.report = report
	watch.value = value
	watch.closed = make(chan struct{})
}

// Value is the acquisition revision before Start and the latest delivered revision afterward.
func (watch *watchLifecycle[T, L]) Value() T {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	return watch.value
}

// Closed is closed when the watch terminates.
func (watch *watchLifecycle[T, L]) Closed() <-chan struct{} { return watch.closed }

// End returns the terminal result once Closed is closed.
func (watch *watchLifecycle[T, L]) End() WatchEnd {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.end == nil {
		return WatchEnd{}
	}
	return *watch.end
}

func (watch *watchLifecycle[T, L]) useScheduler(scheduler *DeliveryScheduler) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.started || watch.hasPending() || watch.end != nil {
		panic(errors.New("Watch deliveries are moved before it is advanced or started"))
	}
	watch.scheduler = scheduler.pending
}

func (watch *watchLifecycle[T, L]) start(listener L) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.started {
		panic(errors.New("Watch is already started"))
	}
	if watch.end != nil {
		panic(errors.New("Watch is stopped"))
	}
	watch.started = true
	watch.listener, watch.listening = listener, true
	if watch.hasPending() {
		watch.scheduleLocked()
	}
}

// Stop idempotently stops future callbacks and returns the terminal result.
func (watch *watchLifecycle[T, L]) Stop() (WatchEnd, error) {
	watch.terminate(WatchEnd{Reason: WatchStopped})
	<-watch.closed
	return watch.End(), nil
}

// ObserveCancellation ends the watch with reason cancelled when ctx is done; it is installed once.
func (watch *watchLifecycle[T, L]) ObserveCancellation(ctx context.Context) {
	watch.mu.Lock()
	if watch.stopAfter != nil {
		watch.mu.Unlock()
		panic(errors.New("Watch cancellation is already installed"))
	}
	if watch.end != nil {
		watch.mu.Unlock()
		return
	}
	watch.stopAfter = context.AfterFunc(ctx, watch.Cancel)
	watch.mu.Unlock()
}

// Cancel ends the watch cancelled: its acquisition context was cancelled.
func (watch *watchLifecycle[T, L]) Cancel() {
	watch.terminate(WatchEnd{Reason: WatchCancelled})
}

// SourceEnded is the source closing: the watch ends ended, or failed with the error when the source failed, at once,
// dropping what is pending.
func (watch *watchLifecycle[T, L]) SourceEnded(failure error) {
	if failure != nil {
		watch.terminate(WatchEnd{Reason: WatchFailed, Error: failure})
		return
	}
	watch.terminate(WatchEnd{Reason: WatchEnded})
}

// EndAfterDelivery tells the watch that its source sends nothing more after its last frames, such as a retirement: it takes
// no more frames, delivers what is pending, then ends ended, after the running call returns, if one runs, and at once when
// nothing is pending and no call runs. Until then the watch stays attached, so SourceEnded, Stop, cancellation and a
// failing listener still end it first; a watch never started keeps what is pending until one of them or Start.
func (watch *watchLifecycle[T, L]) EndAfterDelivery() {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.end != nil || watch.ending {
		return
	}
	watch.ending = true
	if !watch.running && !watch.hasPending() {
		watch.terminateLocked(WatchEnd{Reason: WatchEnded})
	}
}

// open reports whether the watch still takes frames: it has not ended, and its source has not ended. Called under mu.
func (watch *watchLifecycle[T, L]) open() bool { return watch.end == nil && !watch.ending }

// deliverLocked starts delivery of the oldest pending frame when no callback is in flight. Upstream's commit publishes
// before its promise resolves and the drain microtask that advance queues enters the listener before the commit's
// caller resumes, so the frame is taken into delivery before advance returns to the publishing commit; a later Stop
// prevents only frames behind it. The listener itself runs on a delivery goroutine, never inline.
func (watch *watchLifecycle[T, L]) deliverLocked() {
	if watch.running || watch.end != nil || !watch.hasPending() {
		return
	}
	if !watch.holdQueued && watch.scheduler.holding(watch.deliverHeld) {
		watch.holdQueued = true
		return
	}
	if watch.holdQueued {
		return
	}
	frame, listener := watch.takeLocked()
	watch.scheduler.schedule(func() { watch.drain(frame, listener) })
}

// deliverHeld starts the delivery a Hold postponed, with the frames advanced since folded.
func (watch *watchLifecycle[T, L]) deliverHeld() {
	watch.mu.Lock()
	watch.holdQueued = false
	watch.deliverLocked()
	watch.mu.Unlock()
}

// scheduleLocked queues a drain of the frames buffered before Start, as upstream's start queues a microtask.
func (watch *watchLifecycle[T, L]) scheduleLocked() {
	if watch.scheduled || watch.running || watch.end != nil {
		return
	}
	watch.scheduled = true
	watch.scheduler.schedule(func() {
		watch.mu.Lock()
		watch.scheduled = false
		if watch.running || watch.end != nil || !watch.started || !watch.hasPending() {
			watch.finishIfReadyLocked()
			watch.mu.Unlock()
			return
		}
		frame, listener := watch.takeLocked()
		watch.mu.Unlock()
		watch.drain(frame, listener)
	})
}

// takeLocked marks a callback in flight and makes the next delivery the watch value. It also returns the
// listener, which upstream's #drain captures in the same synchronous step that shifts the frame, so a Stop that
// drops the watch's listener reference afterwards does not discard the frame already in delivery.
func (watch *watchLifecycle[T, L]) takeLocked() (watchFrame[T], *L) {
	watch.running = true
	frame, _ := watch.next()
	watch.value = frame.value
	if !watch.listening {
		return frame, nil
	}
	listener := watch.listener
	return frame, &listener
}

// drain delivers frame, which takeLocked took, then every frame buffered behind it until the watch ends.
func (watch *watchLifecycle[T, L]) drain(frame watchFrame[T], listener *L) {
	watch.mu.Lock()
	for listener != nil {
		invoke := watch.invoke
		watch.mu.Unlock()
		err := callWatchListener(invoke, *listener, frame)
		watch.mu.Lock()
		if err != nil {
			if watch.end == nil {
				if report := watch.report; report != nil {
					watch.mu.Unlock()
					report(err)
					watch.mu.Lock()
				}
				if watch.end == nil {
					watch.terminateLocked(WatchEnd{Reason: WatchListenerError, Error: err})
				}
			}
			break
		}
		if watch.end != nil {
			break
		}
		if !watch.hasPending() {
			if watch.ending {
				watch.terminateLocked(WatchEnd{Reason: WatchEnded})
			}
			break
		}
		frame, listener = watch.takeLocked()
	}
	watch.running = false
	if watch.end == nil && watch.hasPending() {
		watch.scheduleLocked()
	}
	watch.finishIfReadyLocked()
	watch.mu.Unlock()
}

// callWatchListener delivers one frame with the commit's values but without its cancellation, converting a panic into
// a listener error.
func callWatchListener[T, L any](invoke func(L, watchFrame[T]) error, listener L, frame watchFrame[T]) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
				return
			}
			err = fmt.Errorf("%v", recovered)
		}
	}()
	frame.ctx = context.WithoutCancel(frame.ctx)
	return invoke(listener, frame)
}

func (watch *watchLifecycle[T, L]) terminate(end WatchEnd) {
	watch.mu.Lock()
	watch.terminateLocked(end)
	watch.mu.Unlock()
}

// terminateLocked ends the watch once: detach, drop pending frames and every reference into the source, and settle
// Closed. A callback already running keeps running.
func (watch *watchLifecycle[T, L]) terminateLocked(end WatchEnd) {
	if watch.end != nil {
		return
	}
	watch.end = &end
	detach := watch.detach
	watch.detach = nil
	watch.report = nil
	var noListener L
	watch.listener, watch.listening = noListener, false
	watch.scheduler = nil
	watch.release()
	if detach != nil && !watch.detached {
		watch.detached = true
		watch.mu.Unlock()
		detach()
		watch.mu.Lock()
	}
	watch.finishIfReadyLocked()
}

func (watch *watchLifecycle[T, L]) finishIfReadyLocked() {
	if watch.resolved || watch.end == nil {
		return
	}
	watch.resolved = true
	if watch.stopAfter != nil {
		watch.stopAfter()
		watch.stopAfter = nil
	}
	//portlint:allow doubleclose closed once: resolved is set under mu before the close, as watch.ts #terminate settles closed once
	close(watch.closed)
}

// Watch is the watch handle of a JSON source. Frames that arrive before Start or while the listener runs wait in one
// delta.PendingOps queue; the next call gets the newest value, the folded ops of every frame since the previous call (or
// [["r", newest]] after the queue overflowed), Retired when any of them was a retirement, and the newest frame's context.
// A source creates one per watch and feeds it with Advance, EndAfterDelivery, SourceEnded and ObserveCancellation.
type Watch[T any] struct {
	watchLifecycle[T, func(ctx context.Context, change WatchChange[T]) error]
	ops delta.PendingOps
	// newest is the newest frame, while a call is pending.
	newest *watchNewest[T]
	// retired is set while a pending frame was a retirement.
	retired bool
}

// watchNewest is the newest frame of a Watch.
type watchNewest[T any] struct {
	value T
	ctx   context.Context
}

// NewWatch returns a watch holding value, as Pi's `new Watch(value, detach, report)`. detach runs once when the watch
// ends; report receives a listener's failure, which also ends the watch. Its deliveries run on a process-wide scheduler
// until On gives it the host's own.
func NewWatch[T any](value T, detach func(), report func(error)) *Watch[T] {
	watch := &Watch[T]{}
	watch.setup(value, detach, report)
	watch.invoke = func(listener func(context.Context, WatchChange[T]) error, frame watchFrame[T]) error {
		return listener(frame.ctx, WatchChange[T]{Value: frame.value, Ops: frame.ops, Retired: frame.retired})
	}
	watch.next = watch.nextDelivery
	watch.hasPending = func() bool { return watch.newest != nil }
	watch.release = func() {
		watch.ops.Take()
		watch.newest = nil
		watch.retired = false
	}
	return watch
}

// On runs the watch's deliveries on scheduler, the Go counterpart of the host's microtask queue. It is called once,
// before the watch is advanced or started, and returns the watch.
func (watch *Watch[T]) On(scheduler *DeliveryScheduler) *Watch[T] {
	watch.useScheduler(scheduler)
	return watch
}

// Start installs the sole asynchronous listener; it never invokes it inline. It panics when the watch is started or
// stopped, as upstream throws.
func (watch *Watch[T]) Start(listener func(ctx context.Context, change WatchChange[T]) error) {
	watch.start(listener)
}

// Advance queues the frame change: its Ops turn the previous value into its Value, and Retired says the document the
// watch follows was retired, which the next call reports however many frames it folds. ctx is the frame's context.
func (watch *Watch[T]) Advance(ctx context.Context, change WatchChange[T]) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if !watch.open() {
		return
	}
	watch.ops.Push(change.Ops)
	watch.newest = &watchNewest[T]{value: change.Value, ctx: ctx}
	if change.Retired {
		watch.retired = true
	}
	if watch.started {
		watch.deliverLocked()
	}
}

func (watch *Watch[T]) nextDelivery() (watchFrame[T], bool) {
	newest := watch.newest
	if newest == nil {
		return watchFrame[T]{}, false
	}
	watch.newest = nil
	retired := watch.retired
	watch.retired = false
	taken := watch.ops.Take()
	ops := taken.Ops
	if taken.Replace {
		// Values of a JSON watch are JSON; T is the source's narrower or wider view of them (a typed document).
		var payload any = newest.value
		ops = []delta.Op{{"r", payload}}
	}
	return watchFrame[T]{value: newest.value, ops: ops, ctx: newest.ctx, retired: retired}, true
}

// NativeWatch is the watch handle of a native source: every value in order, one call each; past 100 pending values they
// collapse to one, replace() or by default the newest value.
type NativeWatch[T any] struct {
	watchLifecycle[T, func(ctx context.Context, change NativeWatchChange[T]) error]
	values  []watchFrame[T]
	replace func() T
}

// NewNativeWatch returns a watch holding value; detach, report and the scheduler as for NewWatch. replace, when not nil,
// gives the value an overflow delivers; nil delivers the newest value.
func NewNativeWatch[T any](value T, detach func(), report func(error), replace func() T) *NativeWatch[T] {
	watch := &NativeWatch[T]{replace: replace}
	watch.setup(value, detach, report)
	watch.invoke = func(listener func(context.Context, NativeWatchChange[T]) error, frame watchFrame[T]) error {
		return listener(frame.ctx, NativeWatchChange[T]{Value: frame.value})
	}
	watch.next = func() (watchFrame[T], bool) {
		if len(watch.values) == 0 {
			return watchFrame[T]{}, false
		}
		frame := watch.values[0]
		watch.values = watch.values[1:]
		return frame, true
	}
	watch.hasPending = func() bool { return len(watch.values) > 0 }
	watch.release = func() {
		watch.values = nil
		watch.replace = nil
	}
	return watch
}

// On runs the watch's deliveries on scheduler; see Watch.On.
func (watch *NativeWatch[T]) On(scheduler *DeliveryScheduler) *NativeWatch[T] {
	watch.useScheduler(scheduler)
	return watch
}

// Start installs the sole asynchronous listener; it never invokes it inline. It panics when the watch is started or
// stopped, as upstream throws.
func (watch *NativeWatch[T]) Start(listener func(ctx context.Context, change NativeWatchChange[T]) error) {
	watch.start(listener)
}

// Advance queues value, the source's new value.
func (watch *NativeWatch[T]) Advance(ctx context.Context, value T) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if !watch.open() {
		return
	}
	if len(watch.values) < maxPendingNativeValues {
		watch.values = append(watch.values, watchFrame[T]{value: value, ctx: ctx})
	} else {
		watch.values = watch.values[:0]
		// Upstream: this.#replace === undefined ? value : this.#replace().
		replacement := value
		if watch.replace != nil {
			replacement = watch.replace()
		}
		watch.values = append(watch.values, watchFrame[T]{value: replacement, ctx: ctx})
	}
	if watch.started {
		watch.deliverLocked()
	}
}
