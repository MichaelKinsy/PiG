package chord

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// Ports packages/chord/src/watch.ts (Watch, NativeWatch, NativeWatchHandle and their shared lifecycle).

// maxPendingWatchFrames is the maximum number of undelivered frames retained behind one unavailable watch listener.
const maxPendingWatchFrames = 100

// retirementOperations is the canonical terminal update of a retired source.
func retirementOperations() []delta.Op { return []delta.Op{{"r", nil}} }

// isRetiredValue reports upstream's `value === null` for a nil map, pointer, slice, or interface.
func isRetiredValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice, reflect.Interface:
		return reflected.IsNil()
	}
	return false
}

// NativeWatchHandle is a serialized observation of an in-process value: as WatchHandle, but a change is a new value
// without ops.
type NativeWatchHandle[T any] interface {
	// Value is the acquisition value before Start and the latest delivered value afterward.
	Value() T
	// Start installs the sole asynchronous listener. It never invokes it inline.
	Start(listener func(ctx context.Context, value T) error)
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

// defaultDeliveries runs the deliveries of a watch that no host moved to its own scheduler (Watch.On, NativeWatch.On).
var defaultDeliveries = NewDeliveryScheduler()

// NewDeliveryScheduler returns an idle delivery scheduler.
func NewDeliveryScheduler() *DeliveryScheduler { return &DeliveryScheduler{pending: newPending()} }

// Wait blocks until no scheduled delivery is queued or running.
func (s *DeliveryScheduler) Wait() { s.pending.wait() }

// AwaitEntered blocks until every delivery scheduled before the call has entered its listener, up to the listener's
// first wait; it returns the cause of ctx when ctx ends the wait.
func (s *DeliveryScheduler) AwaitEntered(ctx context.Context) error {
	return s.pending.awaitEntered(ctx)
}

type watchFrame[T any] struct {
	value T
	ops   []delta.Op
	ctx   context.Context
	// retires marks the retirement frame: the watch ends retired once it is delivered.
	retires bool
}

// watchLifecycle is the lifecycle both handles share: pending frames, serialized delivery, and termination. A source
// feeds it and ends it explicitly; termination drops every reference the watch holds into its source, so a stopped
// watch that is still referenced keeps nothing but its last value alive. The handles supply only how a listener is
// called (invoke) and the ops of an overflow frame (overflowOps).
type watchLifecycle[T, L any] struct {
	mu        sync.Mutex
	scheduler *pending
	detach    func()
	replace   func() T
	// report receives a listener's failure, which also ends the watch; nil drops it.
	report      func(error)
	invoke      func(listener L, frame watchFrame[T]) error
	overflowOps func(value T) []delta.Op
	pending     []watchFrame[T]
	closed      chan struct{}
	value       T
	listener    L
	listening   bool
	started     bool
	scheduled   bool
	running     bool
	detached    bool
	retired     bool
	end         *WatchEnd
	resolved    bool
	stopAfter   func() bool
}

func (watch *watchLifecycle[T, L]) setup(value T, detach func(), report func(error), replace func() T) {
	watch.scheduler = defaultDeliveries.pending
	watch.detach = detach
	watch.report = report
	watch.replace = replace
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
	if watch.started || len(watch.pending) > 0 || watch.end != nil {
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
	if len(watch.pending) > 0 {
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

// SourceEnded is the source closing: the watch ends ended, or failed with the error when the source failed.
func (watch *watchLifecycle[T, L]) SourceEnded(failure error) {
	if failure != nil {
		watch.terminate(WatchEnd{Reason: WatchFailed, Error: failure})
		return
	}
	watch.terminate(WatchEnd{Reason: WatchEnded})
}

// enqueue queues one frame; past the bound the backlog collapses to one frame of replace() or the newest value.
func (watch *watchLifecycle[T, L]) enqueue(ctx context.Context, value T, ops []delta.Op) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.end != nil || watch.retired {
		return
	}
	if len(watch.pending) >= maxPendingWatchFrames {
		watch.pending = watch.pending[:0]
		// Upstream: this.#replace?.() ?? value.
		replacement := value
		if watch.replace != nil {
			if replaced := watch.replace(); !isRetiredValue(replaced) {
				replacement = replaced
			}
		}
		watch.pending = append(watch.pending, watchFrame[T]{value: replacement, ops: watch.overflowOps(replacement), ctx: ctx})
	} else {
		watch.pending = append(watch.pending, watchFrame[T]{value: value, ops: ops, ctx: ctx})
	}
	if watch.started {
		watch.deliverLocked()
	}
}

// enqueueRetirement queues the last frame, after which the watch ends retired. An overflowing backlog is dropped, since
// the frame replaces the root anyway.
func (watch *watchLifecycle[T, L]) enqueueRetirement(ctx context.Context, value T, ops []delta.Op) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.end != nil || watch.retired {
		return
	}
	watch.retired = true
	if len(watch.pending) >= maxPendingWatchFrames {
		watch.pending = watch.pending[:0]
	}
	watch.pending = append(watch.pending, watchFrame[T]{value: value, ops: ops, ctx: ctx, retires: true})
	if watch.started {
		watch.deliverLocked()
	}
}

// deliverLocked starts delivery of the oldest pending frame when no callback is in flight. Upstream's commit publishes
// before its promise resolves and the drain microtask that advance queues enters the listener before the commit's
// caller resumes, so the frame is taken into delivery before advance returns to the publishing commit; a later Stop
// prevents only frames behind it. The listener itself runs on a delivery goroutine, never inline.
func (watch *watchLifecycle[T, L]) deliverLocked() {
	if watch.running || watch.end != nil || len(watch.pending) == 0 {
		return
	}
	frame, listener := watch.takeLocked()
	watch.scheduler.schedule(func() { watch.drain(frame, listener) })
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
		if watch.running || watch.end != nil || !watch.started || len(watch.pending) == 0 {
			watch.finishIfReadyLocked()
			watch.mu.Unlock()
			return
		}
		frame, listener := watch.takeLocked()
		watch.mu.Unlock()
		watch.drain(frame, listener)
	})
}

// takeLocked marks a callback in flight and makes the oldest pending frame the watch value. It also returns the
// listener, which upstream's #drain captures in the same synchronous step that shifts the frame, so a Stop that
// drops the watch's listener reference afterwards does not discard the frame already in delivery.
func (watch *watchLifecycle[T, L]) takeLocked() (watchFrame[T], *L) {
	watch.running = true
	frame := watch.pending[0]
	watch.pending = watch.pending[1:]
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
		if frame.retires {
			watch.terminateLocked(WatchEnd{Reason: WatchRetired})
			break
		}
		if watch.end != nil || len(watch.pending) == 0 {
			break
		}
		frame, listener = watch.takeLocked()
	}
	watch.running = false
	if watch.end == nil && len(watch.pending) > 0 {
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
	watch.replace = nil
	var noListener L
	watch.listener, watch.listening = noListener, false
	watch.scheduler = nil
	watch.pending = nil
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

// Watch is the watch handle of a JSON source: exact frames in order, an overflow becomes one root replacement
// [["r", newest]]. A source creates one per watch and feeds it with Advance, Retire, SourceEnded and
// ObserveCancellation.
type Watch[T any] struct {
	watchLifecycle[T, func(ctx context.Context, value T, ops []delta.Op) error]
}

// NewWatch returns a watch holding value, as Pi's `new Watch(value, detach, report, replace?)`. detach runs once when
// the watch ends; report receives a listener's failure, which also ends the watch; replace gives the value an overflow
// delivers, and nil delivers the newest value. Its deliveries run on a process-wide scheduler until On gives it the
// host's own.
func NewWatch[T any](value T, detach func(), report func(error), replace func() T) *Watch[T] {
	watch := &Watch[T]{}
	watch.setup(value, detach, report, replace)
	watch.invoke = func(listener func(context.Context, T, []delta.Op) error, frame watchFrame[T]) error {
		return listener(frame.ctx, frame.value, frame.ops)
	}
	watch.overflowOps = func(value T) []delta.Op {
		// Values of a JSON watch are JSON; T is the source's narrower or wider view of them (a typed document).
		var payload any = value
		if isRetiredValue(value) {
			payload = nil
		}
		return []delta.Op{{"r", payload}}
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
func (watch *Watch[T]) Start(listener func(ctx context.Context, value T, ops []delta.Op) error) {
	watch.start(listener)
}

// Advance queues the frame ops, which turn the previous value into value.
func (watch *Watch[T]) Advance(ctx context.Context, value T, ops []delta.Op) {
	watch.enqueue(ctx, value, ops)
}

// Retire is the source retiring: it queues the last frame, a root replacement with nil ([["r", null]]) and the zero
// value, after which the watch ends retired. An overflowing backlog is dropped, since the frame replaces the root
// anyway.
func (watch *Watch[T]) Retire(ctx context.Context) {
	var retired T
	watch.enqueueRetirement(ctx, retired, retirementOperations())
}

// NativeWatch is the watch handle of a native source: values in order, an overflow collapses to the newest value.
type NativeWatch[T any] struct {
	watchLifecycle[T, func(ctx context.Context, value T) error]
}

// NewNativeWatch returns a watch holding value; detach, report and the scheduler as for NewWatch.
func NewNativeWatch[T any](value T, detach func(), report func(error)) *NativeWatch[T] {
	watch := &NativeWatch[T]{}
	// No replacement: an overflow delivers the newest value.
	watch.setup(value, detach, report, nil)
	watch.invoke = func(listener func(context.Context, T) error, frame watchFrame[T]) error {
		return listener(frame.ctx, frame.value)
	}
	watch.overflowOps = func(T) []delta.Op { return nil }
	return watch
}

// On runs the watch's deliveries on scheduler; see Watch.On.
func (watch *NativeWatch[T]) On(scheduler *DeliveryScheduler) *NativeWatch[T] {
	watch.useScheduler(scheduler)
	return watch
}

// Start installs the sole asynchronous listener; it never invokes it inline. It panics when the watch is started or
// stopped, as upstream throws.
func (watch *NativeWatch[T]) Start(listener func(ctx context.Context, value T) error) {
	watch.start(listener)
}

// Advance queues value, the source's new value.
func (watch *NativeWatch[T]) Advance(ctx context.Context, value T) {
	watch.enqueue(ctx, value, nil)
}
