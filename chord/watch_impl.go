package chord

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/watchentered"
)

// Ports packages/chord/src/watch.ts (Watch, NativeWatch, NativeWatchHandle and their shared lifecycle).

// maxPendingNativeValues is the maximum number of undelivered values retained behind one unavailable native watch listener.
const maxPendingNativeValues = 100

// NativeWatchHandle is a serialized observation of an in-process value: as WatchHandle, but a value call carries no ops.
type NativeWatchHandle[T any] interface {
	// Value is the acquisition value before Start and the latest delivered value afterward (a failed call keeps it).
	Value() T
	// Start installs the sole asynchronous listener. It never invokes it inline.
	// pig divergence (D224): a listener that blocks on something the host cannot see (a channel, a lock, I/O) calls DeliveryEntered
	// first, or blocks on a goroutine of its own: the waits and the publications that follow its delivery wait until its call returns
	// or it has entered (see DeliveryEntered).
	Start(listener func(ctx context.Context, call NativeWatchCall[T]) error)
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

// delivery is one listener call a watch owes: the first call of a delivery goroutine, or a frame a running drain takes next.
// entered is closed once the call's synchronous part ran (the call returned), the listener reached a wait of the host that
// said so (DeliveryEntered), or the call will not be made; done once the delivery is complete.
type delivery struct {
	entered  chan struct{}
	enter    sync.Once
	done     chan struct{}
	complete sync.Once
	// root is the first delivery of the goroutine that makes this call: the calls of one drain follow each other on one goroutine,
	// so a listener that awaits the deliveries cannot wait for a later call of its own drain.
	root *delivery
	// behind is the call of the same drain this one follows, nil for the first. ret is closed once a call returned (or will not be
	// made); a call entered without it is suspended in a wait of the host, and the call behind it waits for it to resume, as the
	// next frame of Pi's drain does for a listener that awaits: nothing that awaits the deliveries waits for that call.
	behind  *delivery
	ret     chan struct{}
	retOnce sync.Once
}

// returned reports whether the call returned, or will not be made.
func (d *delivery) returned() bool {
	select {
	case <-d.ret:
		return true
	default:
		return false
	}
}

// MarkEntered closes entered, once.
func (d *delivery) MarkEntered() { d.enter.Do(func() { close(d.entered) }) }

// hasEntered reports whether the call entered.
func (d *delivery) hasEntered() bool {
	select {
	case <-d.entered:
		return true
	default:
		return false
	}
}

// finish marks the delivery returned, entered and complete.
func (d *delivery) finish() {
	d.retOnce.Do(func() { close(d.ret) })
	d.MarkEntered()
	d.complete.Do(func() { close(d.done) })
}

// DeliveryEntered says that the watch delivery running on the calling goroutine, if there is one, has entered: its listener
// went on to wait for something the host knows (a wait of the Durable Session, a park of the host loop, a call that awaits the
// deliveries of earlier batches), as an upstream listener's first await ends the synchronous part DeliveryScheduler.AwaitEntered
// orders. A wait that the host cannot see (a plain lock or channel of the listener's) does not call it, so the listener calls it
// first when it blocks on one: the contract of every watch listener (pig divergence (D224)).
//
// pig divergence (D224): Go has no await point, so a listener's first suspension is observable only by a call like this one.
func DeliveryEntered() { watchentered.Entered() }

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

// schedule runs job off the caller's goroutine, as upstream's queueMicrotask runs it outside the publishing call. job calls
// d.MarkEntered once the synchronous part of its listener ran; awaitEntered waits for that.
func (tracker *pending) schedule(job func(d *delivery)) *delivery {
	d := tracker.register(nil)
	tracker.begin()
	//portlint:allow asyncorder joined by pending.wait (begin/end count); watch frames are taken under watch.mu in publish order and one runs at a time, as watch.ts #schedule/#drain
	go func() { //portlint:allow golifetime the pending count (begin/end) is the join: pending.wait returns once every scheduled drain ended (watch.ts #schedule/#drain)
		unregister := watchentered.Register(d)
		defer func() {
			unregister()
			d.finish()
			tracker.end()
		}()
		job(d)
	}()
	return d
}

// register records a listener call the caller owes, so that awaitEntered waits for it until it is entered. behind is the call
// of the drain that makes it which it follows, nil for a call that starts a drain.
func (tracker *pending) register(behind *delivery) *delivery {
	d := &delivery{entered: make(chan struct{}), done: make(chan struct{}), ret: make(chan struct{}), behind: behind}
	if behind == nil {
		d.root = d
	} else {
		d.root = behind.root
	}
	tracker.mu.Lock()
	tracker.deliveries = append(slices.DeleteFunc(tracker.deliveries, (*delivery).finished), d)
	tracker.mu.Unlock()
	return d
}

// awaitEntered blocks until every delivery scheduled before the call has entered: the synchronous part of its listener ran (the
// call returned), or the listener went on to wait for something the host knows (DeliveryEntered), or the delivery ended. Upstream's
// drain microtask runs a listener up to its first await before the code that awaited a commit's promise resumes, and before the
// Session's macrotask hop to the next publication of a call; a Go caller that resumes on another goroutine awaits the deliveries
// it would otherwise outrun.
//
// pig divergence (D224): a listener that blocks on a wait the host cannot see holds this wait back until it returns; Pi's listener
// awaits and nothing waits for it. The listener's contract is DeliveryEntered's.
//
// A call made from inside a delivery (a listener that waits for the Harness) is itself such a wait: it marks its own delivery
// entered and does not wait for it, so listeners that wait for the Harness do not wait for each other. A listener that blocks
// on a plain lock or channel the host cannot see is not entered until it returns: if the caller holds that lock the wait does
// not return (upstream has no locks; the listener's other blocking calls go through the Session's waits, which are marked). It
// reads no goroutine state beyond the caller's own ID: one channel receive per delivery, whatever the number of goroutines. It
// returns the cause of ctx when ctx ends the wait.
func (tracker *pending) awaitEntered(ctx context.Context) error {
	var own *delivery
	if d, ok := watchentered.Current(); ok {
		own, _ = d.(*delivery)
		d.MarkEntered()
	}
	tracker.mu.Lock()
	scheduled := slices.DeleteFunc(slices.Clone(tracker.deliveries), (*delivery).finished)
	tracker.mu.Unlock()
	for _, d := range scheduled {
		if own != nil && d.root == own.root {
			continue
		}
		// A delivery that entered is entered for a caller whose ctx ended since, as an awaited promise that resolved; a cancelled ctx
		// ends the wait for one that has not.
		if d.hasEntered() {
			continue
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if behind := d.behind; behind != nil {
			select {
			case <-d.entered:
				continue
			case <-behind.entered:
			case <-ctx.Done():
				if d.hasEntered() {
					continue
				}
				return context.Cause(ctx)
			}
			if !behind.returned() {
				// The call before it is suspended in a wait of the host: this one starts when that resumes, not before the code
				// that awaits the deliveries.
				continue
			}
		}
		select {
		case <-d.entered:
		case <-ctx.Done():
			if !d.hasEntered() {
				return context.Cause(ctx)
			}
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

// Busy reports whether a delivery is queued or running: what Wait would wait for.
func (s *DeliveryScheduler) Busy() bool {
	s.pending.mu.Lock()
	defer s.pending.mu.Unlock()
	return s.pending.count != 0
}

// Hold makes the deliveries that frames advanced from now on would start wait until the returned release runs, which
// starts them in the order the watches were first advanced. Frames a watch is advanced with meanwhile fold into one call,
// as the frames of one publication fold into the single microtask upstream queues for it. Holds nest; release is called
// once.
func (s *DeliveryScheduler) Hold() (release func()) { return s.pending.hold() }

// AwaitEntered blocks until every delivery scheduled before the call has entered: the synchronous part of its listener ran (the
// call returned), or the listener went on to a wait the host knows (DeliveryEntered), or the delivery ended. A call made from inside
// a delivery enters that delivery and does not wait for it. A listener blocked on a plain lock or channel the host cannot see has
// not entered until it returns. It returns the cause of ctx when ctx ends the wait.
func (s *DeliveryScheduler) AwaitEntered(ctx context.Context) error {
	return s.pending.awaitEntered(ctx)
}

// watchFrame is one listener call: the value it delivers, the ops and retirement flag of a JSON watch, or the failure it
// reports (failed, with err), and the context of the newest frame it covers. The listener gets the call and the context
// apart: a call is plain data.
type watchFrame[T any] struct {
	value   T
	ops     []delta.Op
	ctx     context.Context
	retired bool
	failed  bool
	err     error
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
	// current is the call the running drain makes, or will make first; queued is the registered call it makes next, set while a
	// frame waits behind the running call, so that awaitEntered sees every call that is not yet entered.
	current *delivery
	queued  *delivery
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

// ErrWatchStopped is the panic of Start on a watch that already ended (Pi's `Watch is stopped` throw): a caller whose watch may end
// before it starts it (a holder released the reference it follows) recovers it.
var ErrWatchStopped = errors.New("Watch is stopped")

// StartUnlessEnded runs start, which starts a watch, and says false when the watch had ended already (it panicked with ErrWatchStopped):
// a watch whose context was cancelled or whose holder released it between its making and its start has nothing to follow.
func StartUnlessEnded(start func()) (started bool) {
	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(error); ok && errors.Is(err, ErrWatchStopped) {
				started = false
				return
			}
			panic(r)
		}
	}()
	start()
	return true
}

func (watch *watchLifecycle[T, L]) start(listener L) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.started {
		panic(errors.New("Watch is already started"))
	}
	if watch.end != nil {
		panic(ErrWatchStopped)
	}
	watch.started = true
	watch.listener, watch.listening = listener, true
	if watch.hasPending() {
		watch.scheduleLocked()
	}
}

// Stop idempotently stops future callbacks and returns the terminal result.
func (watch *watchLifecycle[T, L]) Stop() (WatchEnd, error) {
	watch.terminate(WatchEnd{Kind: WatchStopped})
	<-watch.closed
	return watch.End(), nil
}

// ObserveAbort ends the watch aborted when ctx is done; it is installed once.
func (watch *watchLifecycle[T, L]) ObserveAbort(ctx context.Context) {
	watch.mu.Lock()
	if watch.stopAfter != nil {
		watch.mu.Unlock()
		panic(errors.New("Watch abort is already installed"))
	}
	if watch.end != nil {
		watch.mu.Unlock()
		return
	}
	watch.stopAfter = context.AfterFunc(ctx, watch.Abort)
	watch.mu.Unlock()
}

// Abort ends the watch aborted: its acquisition context was aborted.
func (watch *watchLifecycle[T, L]) Abort() {
	watch.terminate(WatchEnd{Kind: WatchAborted})
}

// EndWith ends the watch with end at once, dropping what is pending: the source closed (WatchEnded), or failed for good
// (WatchFailed). A failure the source may recover from is a failed call instead (Fail). It is Pi's `end(end)`; Go's End
// is the accessor of the terminal result.
func (watch *watchLifecycle[T, L]) EndWith(end WatchEnd) {
	watch.terminate(end)
}

// EndAfterDelivery tells the watch that its source sends nothing more after its last frames, such as a retirement: it takes
// no more frames, delivers what is pending, then ends ended, after the running call returns, if one runs, and at once when
// nothing is pending and no call runs. Until then the watch stays attached, so EndWith, Stop, an abort and a
// failing listener still end it first; a watch never started keeps what is pending until one of them or Start.
func (watch *watchLifecycle[T, L]) EndAfterDelivery() {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.end != nil || watch.ending {
		return
	}
	watch.ending = true
	if !watch.running && !watch.hasPending() {
		watch.terminateLocked(WatchEnd{Kind: WatchEnded})
	}
}

// open reports whether the watch still takes frames: it has not ended, and its source has not ended. Called under mu.
func (watch *watchLifecycle[T, L]) open() bool { return watch.end == nil && !watch.ending }

// deliverLocked starts delivery of the oldest pending frame when no callback is in flight. Upstream's commit publishes
// before its promise resolves and the drain microtask that advance queues enters the listener before the commit's
// caller resumes, so the frame is taken into delivery before advance returns to the publishing commit; a later Stop
// prevents only frames behind it. The listener itself runs on a delivery goroutine, never inline.
func (watch *watchLifecycle[T, L]) deliverLocked() {
	if watch.end != nil || !watch.hasPending() {
		return
	}
	if watch.running {
		// The running drain takes this frame itself after its call returns: register the call now, as the drain microtask a
		// publication queues is visible to the code that awaits the commit before the drain runs it.
		if watch.queued == nil {
			watch.queued = watch.scheduler.register(watch.current)
		}
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
	watch.current = watch.scheduler.schedule(func(d *delivery) { watch.drain(d, frame, listener) })
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
	watch.current = watch.scheduler.schedule(func(d *delivery) {
		watch.mu.Lock()
		watch.scheduled = false
		if watch.running || watch.end != nil || !watch.started || !watch.hasPending() {
			watch.finishIfReadyLocked()
			watch.mu.Unlock()
			return
		}
		frame, listener := watch.takeLocked()
		watch.mu.Unlock()
		watch.drain(d, frame, listener)
	})
}

// takeLocked marks a callback in flight and makes the next delivery the watch value. It also returns the
// listener, which upstream's #drain captures in the same synchronous step that shifts the frame, so a Stop that
// drops the watch's listener reference afterwards does not discard the frame already in delivery.
func (watch *watchLifecycle[T, L]) takeLocked() (watchFrame[T], *L) {
	watch.running = true
	frame, _ := watch.next()
	if !frame.failed {
		watch.value = frame.value
	}
	if !watch.listening {
		return frame, nil
	}
	listener := watch.listener
	return frame, &listener
}

// drain delivers frame, which takeLocked took, then every frame buffered behind it until the watch ends.
func (watch *watchLifecycle[T, L]) drain(d *delivery, frame watchFrame[T], listener *L) {
	completed := false
	defer func() {
		if completed {
			return
		}
		// The goroutine ended inside a listener (runtime.Goexit, as t.FailNow does) with the call in flight, so the deliveries this
		// drain registered are finished here, as a thrown error ends the watch upstream: nothing that awaits them waits for ever.
		watch.mu.Lock()
		defer watch.mu.Unlock()
		d.finish()
		if queued := watch.queued; queued != nil {
			watch.queued = nil
			queued.finish()
		}
		watch.running = false
		if watch.end == nil {
			watch.terminateLocked(WatchEnd{Kind: WatchListenerError, Error: errors.New("a watch listener ended its goroutine")})
		}
	}()
	watch.mu.Lock()
	for listener != nil {
		invoke := watch.invoke
		watch.mu.Unlock()
		err := callWatchListener(invoke, *listener, frame)
		// The synchronous part of the call the delivery was scheduled for ran: the code that awaited it may resume.
		d.finish()
		watch.mu.Lock()
		if err != nil {
			if watch.end == nil {
				if report := watch.report; report != nil {
					watch.mu.Unlock()
					report(err)
					watch.mu.Lock()
				}
				if watch.end == nil {
					watch.terminateLocked(WatchEnd{Kind: WatchListenerError, Error: err})
				}
			}
			break
		}
		if watch.end != nil {
			break
		}
		if !watch.hasPending() {
			if watch.ending {
				watch.terminateLocked(WatchEnd{Kind: WatchEnded})
			}
			break
		}
		frame, listener = watch.takeLocked()
		// The call for this frame was registered when it was published (deliverLocked), or is registered now: either way
		// before mu is released, so awaitEntered sees it.
		previous := d
		d = watch.queued
		watch.queued = nil
		if d == nil {
			d = watch.scheduler.register(previous)
		}
		watch.current = d
		watchentered.Register(d)
	}
	d.finish()
	if queued := watch.queued; queued != nil {
		// The frame it stood for will be delivered by a new drain or not at all.
		watch.queued = nil
		queued.finish()
	}
	watch.running = false
	if watch.end == nil && watch.hasPending() {
		watch.scheduleLocked()
	}
	watch.finishIfReadyLocked()
	completed = true
	watch.mu.Unlock()
}

// callWatchListener delivers one frame with the commit's values but without its abort, converting a panic into
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
// A failure (Fail) drops the pending frames and waits as one failed call; a value after it replaces it, and the first
// value call after a failure is [["r", value]]. A source creates one per watch and feeds it with Advance, Fail,
// EndAfterDelivery, EndWith and ObserveAbort.
type Watch[T any] struct {
	watchLifecycle[T, func(ctx context.Context, call WatchCall[T]) error]
	ops delta.PendingOps
	// newest is the newest value frame, while a value call is pending.
	newest *watchNewest[T]
	// retired is set while a pending frame was a retirement.
	retired bool
	// failure is the newest frame when it was a failure: the next call is this.
	failure *watchFailure
	// replace is set when a failure came after the last value call: the next value call replaces the root, and ops are not
	// kept.
	replace bool
}

// watchNewest is the newest value frame of a Watch.
type watchNewest[T any] struct {
	value T
	ctx   context.Context
}

// watchFailure is a pending failure and its frame's context.
type watchFailure struct {
	err error
	ctx context.Context
}

// NewWatch returns a watch holding value, as Pi's `new Watch(value, detach, report)`. detach runs once when the watch
// ends; report receives a listener's failure, which also ends the watch. Its deliveries run on a process-wide scheduler
// until On gives it the host's own.
func NewWatch[T any](value T, detach func(), report func(error)) *Watch[T] {
	watch := &Watch[T]{}
	watch.setup(value, detach, report)
	watch.invoke = func(listener func(context.Context, WatchCall[T]) error, frame watchFrame[T]) error {
		if frame.failed {
			return listener(frame.ctx, WatchCall[T]{Kind: WatchCallFailed, Error: frame.err})
		}
		return listener(frame.ctx, WatchCall[T]{Kind: WatchCallValue, Value: frame.value, Ops: frame.ops, Retired: frame.retired})
	}
	watch.next = watch.nextDelivery
	watch.hasPending = func() bool { return watch.newest != nil || watch.failure != nil }
	watch.release = func() {
		watch.ops.Take()
		watch.newest = nil
		watch.retired = false
		watch.failure = nil
		watch.replace = false
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
//
// A listener that blocks on something the host cannot see before it returns (a channel send or receive, a mutex, network I/O, a
// child process) holds back every wait that follows its delivery until it returns: the Harness waits, and the Session's
// publication of the next commit of a call, which waits for the synchronous part of each listener (pig divergence (D224): Pi's
// listener awaits, which ends its synchronous part, and nothing waits for it). Such a listener calls DeliveryEntered before it
// blocks, or does the blocking on a goroutine of its own and returns.
func (watch *Watch[T]) Start(listener func(ctx context.Context, call WatchCall[T]) error) {
	watch.start(listener)
}

// Advance queues the frame call. A value call's Ops turn the previous value into its Value, and Retired says the document
// the watch follows was retired, which the next value call reports however many frames it folds; a failed call is Fail.
// ctx is the frame's context.
func (watch *Watch[T]) Advance(ctx context.Context, call WatchCall[T]) {
	if call.Kind == WatchCallFailed {
		watch.Fail(ctx, call.Error)
		return
	}
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if !watch.open() {
		return
	}
	if watch.replace {
		watch.failure = nil
	} else {
		watch.ops.Push(call.Ops)
	}
	watch.newest = &watchNewest[T]{value: call.Value, ctx: ctx}
	if call.Retired {
		watch.retired = true
	}
	if watch.started {
		watch.deliverLocked()
	}
}

// Fail queues a failure the source may recover from: the pending frames are dropped (Value keeps the last delivered value),
// the listener gets a failed call with err, and the next value arrives as [["r", value]]. ctx is the frame's context.
func (watch *Watch[T]) Fail(ctx context.Context, err error) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if !watch.open() {
		return
	}
	watch.ops.Take()
	watch.newest = nil
	watch.failure = &watchFailure{err: err, ctx: ctx}
	watch.replace = true
	if watch.started {
		watch.deliverLocked()
	}
}

func (watch *Watch[T]) nextDelivery() (watchFrame[T], bool) {
	if watch.replace {
		return watch.nextAfterFailure()
	}
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

// nextAfterFailure is the pending failure, or the value after one as a root replacement (no ops were kept since the
// failure).
func (watch *Watch[T]) nextAfterFailure() (watchFrame[T], bool) {
	if failure := watch.failure; failure != nil {
		watch.failure = nil
		return watchFrame[T]{ctx: failure.ctx, failed: true, err: failure.err}, true
	}
	newest := watch.newest
	if newest == nil {
		return watchFrame[T]{}, false
	}
	watch.newest = nil
	retired := watch.retired
	watch.retired = false
	watch.replace = false
	var payload any = newest.value
	return watchFrame[T]{value: newest.value, ops: []delta.Op{{"r", payload}}, ctx: newest.ctx, retired: retired}, true
}

// NativeWatch is the watch handle of a native source: every value in order, one call each; past 100 pending values they
// collapse to one, replace() or by default the newest value. A failure (Fail) drops the pending values and waits as one
// failed call; a value after it replaces it.
type NativeWatch[T any] struct {
	watchLifecycle[T, func(ctx context.Context, call NativeWatchCall[T]) error]
	values  []watchFrame[T]
	replace func() T
	// failure is a pending failure, and nothing else is pending meanwhile.
	failure *watchFrame[T]
}

// NewNativeWatch returns a watch holding value; detach, report and the scheduler as for NewWatch. replace, when not nil,
// gives the value an overflow delivers; nil delivers the newest value.
func NewNativeWatch[T any](value T, detach func(), report func(error), replace func() T) *NativeWatch[T] {
	watch := &NativeWatch[T]{replace: replace}
	watch.setup(value, detach, report)
	watch.invoke = func(listener func(context.Context, NativeWatchCall[T]) error, frame watchFrame[T]) error {
		if frame.failed {
			return listener(frame.ctx, NativeWatchCall[T]{Kind: WatchCallFailed, Error: frame.err})
		}
		return listener(frame.ctx, NativeWatchCall[T]{Kind: WatchCallValue, Value: frame.value})
	}
	watch.next = func() (watchFrame[T], bool) {
		if failure := watch.failure; failure != nil {
			watch.failure = nil
			return *failure, true
		}
		if len(watch.values) == 0 {
			return watchFrame[T]{}, false
		}
		frame := watch.values[0]
		watch.values = watch.values[1:]
		return frame, true
	}
	watch.hasPending = func() bool { return len(watch.values) > 0 || watch.failure != nil }
	watch.release = func() {
		watch.values = nil
		watch.replace = nil
		watch.failure = nil
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
//
// A listener that blocks on something the host cannot see before it returns (a channel send or receive, a mutex, network I/O, a
// child process) holds back every wait that follows its delivery until it returns: the Harness waits, and the Session's
// publication of the next commit of a call, which waits for the synchronous part of each listener (pig divergence (D224): Pi's
// listener awaits, which ends its synchronous part, and nothing waits for it). Such a listener calls DeliveryEntered before it
// blocks, or does the blocking on a goroutine of its own and returns.
func (watch *NativeWatch[T]) Start(listener func(ctx context.Context, call NativeWatchCall[T]) error) {
	watch.start(listener)
}

// Advance queues value, the source's new value; it replaces a pending failure.
func (watch *NativeWatch[T]) Advance(ctx context.Context, value T) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if !watch.open() {
		return
	}
	watch.failure = nil
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

// Fail queues a failure the source may recover from: the pending values are dropped (Value keeps the last delivered value),
// and the listener gets a failed call with err. ctx is the frame's context.
func (watch *NativeWatch[T]) Fail(ctx context.Context, err error) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if !watch.open() {
		return
	}
	watch.values = watch.values[:0]
	watch.failure = &watchFrame[T]{ctx: ctx, failed: true, err: err}
	if watch.started {
		watch.deliverLocked()
	}
}
