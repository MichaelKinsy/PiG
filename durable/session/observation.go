package session

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// Ports packages/durable/src/session/observation.ts

// maxPendingWatchFrames is the maximum number of exact committed frames
// retained behind one unavailable watch listener.
const maxPendingWatchFrames = 100

// retirementOperations is the canonical terminal update for a retired
// document incarnation.
func retirementOperations() []durable.Op { return []durable.Op{{"r", nil}} }

// isRetiredValue reports upstream's `value === null` for a nil map, pointer,
// slice, or interface.
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

// pending counts deliveries upstream runs from the microtask queue, so a
// caller can wait until every scheduled delivery has drained.
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

// schedule runs job off the caller's goroutine, as upstream's queueMicrotask
// runs it outside the publishing call. awaitEntered waits for what it scheduled.
func (tracker *pending) schedule(job func()) {
	d := &delivery{started: make(chan struct{}), done: make(chan struct{})}
	tracker.begin()
	tracker.mu.Lock()
	tracker.deliveries = append(slices.DeleteFunc(tracker.deliveries, (*delivery).finished), d)
	tracker.mu.Unlock()
	//portlint:allow asyncorder joined by pending.wait (begin/end count); watch frames are taken under watch.mu in publish order and one runs at a time, as observation.ts:239-275 #schedule/#drain
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

type watchFrame[T any] struct {
	value T
	ops   []durable.Op
	ctx   context.Context
	// retires marks the retirement frame: the watch ends retired once it is delivered.
	retires bool
}

// CommittedWatch is a serialized exact-frame watch bound to one document incarnation or conversation view. Its source
// ends it explicitly: Retire for a retired document, SourceEnded for a closed or failed Session. Termination drops every
// reference the watch holds into its source, so a stopped watch that is still referenced keeps nothing but its last
// value alive.
type CommittedWatch[T any] struct {
	mu        sync.Mutex
	scheduler *pending
	detach    func()
	replace   func() T
	// report receives a listener's failure, which also ends the watch; nil drops it.
	report    func(error)
	pending   []watchFrame[T]
	closed    chan struct{}
	value     T
	listener  func(ctx context.Context, value T, ops []durable.Op) error
	started   bool
	scheduled bool
	running   bool
	detached  bool
	retired   bool
	end       *durable.WatchEnd
	resolved  bool
	stopAfter func() bool
}

// NewCommittedWatch returns a watch holding value whose deliveries run on session's delivery scheduler. detach runs once
// when the watch ends; replace gives the value an overflow delivers, and nil delivers the newest value. Ports
// observation.ts:174 CommittedWatch constructor (view.ts:104, events.ts:132).
func NewCommittedWatch[T any](session *SessionImpl, value T, detach func(), replace func() T) *CommittedWatch[T] {
	watch := newCommittedWatch(value, detach, replace, session.scheduler)
	watch.report = session.reportListener
	return watch
}

func newCommittedWatch[T any](value T, detach func(), replace func() T, scheduler *pending) *CommittedWatch[T] {
	return &CommittedWatch[T]{scheduler: scheduler, detach: detach, replace: replace, value: value, closed: make(chan struct{})}
}

// Value is the acquisition revision before Start and the latest delivered revision afterward.
func (watch *CommittedWatch[T]) Value() T {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	return watch.value
}

// Closed is closed when the watch terminates.
func (watch *CommittedWatch[T]) Closed() <-chan struct{} { return watch.closed }

// End returns the terminal result once Closed is closed.
func (watch *CommittedWatch[T]) End() durable.WatchEnd {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.end == nil {
		return durable.WatchEnd{}
	}
	return *watch.end
}

// Start installs the sole asynchronous listener; it never invokes it inline.
// It panics when the watch is started or stopped, as upstream throws.
func (watch *CommittedWatch[T]) Start(listener func(ctx context.Context, value T, ops []durable.Op) error) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.started {
		panic(errors.New("Watch is already started"))
	}
	if watch.end != nil {
		panic(errors.New("Watch is stopped"))
	}
	watch.started = true
	watch.listener = listener
	if len(watch.pending) > 0 {
		watch.scheduleLocked()
	}
}

// Stop idempotently stops future callbacks and returns the terminal result.
func (watch *CommittedWatch[T]) Stop() (durable.WatchEnd, error) {
	watch.terminate(durable.WatchEnd{Reason: durable.WatchStopped})
	<-watch.closed
	return watch.End(), nil
}

// ObserveCancellation ends the watch with reason cancelled when ctx is done.
func (watch *CommittedWatch[T]) ObserveCancellation(ctx context.Context) {
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

func (watch *CommittedWatch[T]) Cancel() {
	watch.terminate(durable.WatchEnd{Reason: durable.WatchCancelled})
}

// SourceEnded is the source closing: the watch ends ended, or failed with the error when the Session failed.
func (watch *CommittedWatch[T]) SourceEnded(failure error) {
	if failure != nil {
		watch.terminate(durable.WatchEnd{Reason: durable.WatchFailed, Error: failure})
		return
	}
	watch.terminate(durable.WatchEnd{Reason: durable.WatchEnded})
}

func (watch *CommittedWatch[T]) Advance(ctx context.Context, value T, ops []durable.Op) {
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
		var payload any = replacement
		if isRetiredValue(replacement) {
			payload = nil
		}
		watch.pending = append(watch.pending, watchFrame[T]{value: replacement, ops: []durable.Op{{"r", payload}}, ctx: ctx})
	} else {
		watch.pending = append(watch.pending, watchFrame[T]{value: value, ops: ops, ctx: ctx})
	}
	if watch.started {
		watch.deliverLocked()
	}
}

// Retire is the document incarnation retiring: it queues the last frame, a root replacement with nil ([["r", null]])
// and the zero value, after which the watch ends retired. An overflowing backlog is dropped, since the frame replaces
// the root anyway.
func (watch *CommittedWatch[T]) Retire(ctx context.Context) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.end != nil || watch.retired {
		return
	}
	watch.retired = true
	if len(watch.pending) >= maxPendingWatchFrames {
		watch.pending = watch.pending[:0]
	}
	var retired T
	watch.pending = append(watch.pending, watchFrame[T]{value: retired, ops: retirementOperations(), ctx: ctx, retires: true})
	if watch.started {
		watch.deliverLocked()
	}
}

// deliverLocked starts delivery of the oldest pending frame when no callback is in flight. Upstream's commit publishes
// before its promise resolves and the drain microtask that advance queues enters the listener before the commit's
// caller resumes, so the frame is taken into delivery before Advance returns to the publishing commit; a later Stop
// prevents only frames behind it. The listener itself runs on a delivery goroutine, never inline.
func (watch *CommittedWatch[T]) deliverLocked() {
	if watch.running || watch.end != nil || len(watch.pending) == 0 {
		return
	}
	frame, listener := watch.takeLocked()
	watch.scheduler.schedule(func() { watch.drain(frame, listener) })
}

// scheduleLocked queues a drain of the frames buffered before Start, as upstream's start queues a microtask.
func (watch *CommittedWatch[T]) scheduleLocked() {
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
func (watch *CommittedWatch[T]) takeLocked() (watchFrame[T], func(context.Context, T, []durable.Op) error) {
	watch.running = true
	frame := watch.pending[0]
	watch.pending = watch.pending[1:]
	watch.value = frame.value
	return frame, watch.listener
}

// drain delivers frame, which takeLocked took, then every frame buffered behind it until the watch ends.
func (watch *CommittedWatch[T]) drain(frame watchFrame[T], listener func(context.Context, T, []durable.Op) error) {
	watch.mu.Lock()
	for listener != nil {
		watch.mu.Unlock()
		err := callWatchListener(listener, frame)
		watch.mu.Lock()
		if err != nil {
			if watch.end == nil {
				if report := watch.report; report != nil {
					watch.mu.Unlock()
					report(err)
					watch.mu.Lock()
				}
				if watch.end == nil {
					watch.terminateLocked(durable.WatchEnd{Reason: durable.WatchListenerError, Error: err})
				}
			}
			break
		}
		if frame.retires {
			watch.terminateLocked(durable.WatchEnd{Reason: durable.WatchRetired})
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

// callWatchListener delivers one frame with the commit's values but without
// its cancellation, converting a panic into a listener error.
func callWatchListener[T any](listener func(context.Context, T, []durable.Op) error, frame watchFrame[T]) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
				return
			}
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return listener(context.WithoutCancel(frame.ctx), frame.value, frame.ops)
}

func (watch *CommittedWatch[T]) terminate(end durable.WatchEnd) {
	watch.mu.Lock()
	watch.terminateLocked(end)
	watch.mu.Unlock()
}

func (watch *CommittedWatch[T]) terminateLocked(end durable.WatchEnd) {
	if watch.end != nil {
		return
	}
	watch.end = &end
	detach := watch.detach
	watch.detach = nil
	watch.report = nil
	watch.replace = nil
	watch.listener = nil
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

func (watch *CommittedWatch[T]) finishIfReadyLocked() {
	if watch.resolved || watch.end == nil {
		return
	}
	watch.resolved = true
	if watch.stopAfter != nil {
		watch.stopAfter()
		watch.stopAfter = nil
	}
	close(watch.closed)
}

// DeliveryScheduler runs watch deliveries for a host that has no SessionImpl: each scheduled delivery is queued as upstream's
// microtask is, and Wait returns once every scheduled delivery has drained.
type DeliveryScheduler struct{ pending *pending }

// NewDeliveryScheduler returns an idle delivery scheduler.
func NewDeliveryScheduler() *DeliveryScheduler { return &DeliveryScheduler{pending: newPending()} }

// Wait blocks until no scheduled delivery is queued or running.
func (s *DeliveryScheduler) Wait() { s.pending.wait() }

// NewCommittedWatchOn is NewCommittedWatch for a host whose deliveries run on scheduler (the native Durable core driver).
// report receives a listener's failure, which also ends the watch, as the Session's report does for NewCommittedWatch.
func NewCommittedWatchOn[T any](scheduler *DeliveryScheduler, value T, detach func(), replace func() T, report func(error)) *CommittedWatch[T] {
	watch := newCommittedWatch(value, detach, replace, scheduler.pending)
	watch.report = report
	return watch
}
