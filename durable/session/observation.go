package session

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
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
	mu     sync.Mutex
	count  int
	idle   *sync.Cond
	drains []func()
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

// wait runs the queued state attachment drains, then blocks until no scheduled delivery is queued or running. A drain
// another goroutine took off the queue counts until it finishes, as upstream's flush resolves only after the drain
// microtask ran.
func (tracker *pending) wait() {
	tracker.mu.Lock()
	for {
		if len(tracker.drains) > 0 {
			drains := tracker.drains
			tracker.drains = nil
			tracker.mu.Unlock()
			tracker.run(drains)
			tracker.mu.Lock()
			continue
		}
		if tracker.count == 0 {
			break
		}
		tracker.idle.Wait()
	}
	tracker.mu.Unlock()
}

// queueDrain registers a state attachment drain that the publishing commit runs before it returns. The drain counts as
// a scheduled delivery from now until it finishes.
func (tracker *pending) queueDrain(drain func()) {
	tracker.mu.Lock()
	tracker.count++
	tracker.drains = append(tracker.drains, drain)
	tracker.idle.Broadcast()
	tracker.mu.Unlock()
}

// runDrains runs the queued state attachment drains on the calling goroutine. Upstream's drain microtask enters the
// subscribers after the publishing commit settled and before its caller resumes. The Session line is already free,
// so a subscriber that commits queues behind and a reentrant call returns at once: the draining loop delivers the
// frames the subscriber's commit published.
func (tracker *pending) runDrains() {
	tracker.mu.Lock()
	drains := tracker.drains
	tracker.drains = nil
	tracker.mu.Unlock()
	tracker.run(drains)
}

func (tracker *pending) run(drains []func()) {
	for _, drain := range drains {
		func() {
			defer tracker.end()
			drain()
		}()
	}
}

// schedule runs job off the caller's goroutine, as upstream's queueMicrotask
// runs it outside the publishing call.
func (tracker *pending) schedule(job func()) {
	tracker.begin()
	go func() {
		defer tracker.end()
		job()
	}()
}

// CommittedStateSource is the Session-to-Chord bridge owned one-to-one by one
// attached state: a document, or a conversation view. A nil value retires it.
type CommittedStateSource[T any] struct {
	mu          sync.Mutex
	scheduler   *pending
	attachments map[*sessionSourceAttachment[T]]struct{}
	release     func()
	value       T
	cursor      int
	retired     bool
	closed      bool
}

// NewCommittedStateSource returns a source holding value whose deliveries run on session's delivery scheduler; release
// runs once when the source finishes disposal. Ports observation.ts:24 CommittedStateSource constructor (the Harness
// builds conversation views on it, view.ts:90).
func NewCommittedStateSource[T any](session *SessionImpl, value T, release func()) *CommittedStateSource[T] {
	return newCommittedStateSource(value, release, session.scheduler)
}

func newCommittedStateSource[T any](value T, release func(), scheduler *pending) *CommittedStateSource[T] {
	return &CommittedStateSource[T]{
		scheduler:   scheduler,
		attachments: map[*sessionSourceAttachment[T]]struct{}{},
		release:     release,
		value:       value,
	}
}

// Attach implements chord.ReplicatedStateSource.
func (source *CommittedStateSource[T]) Attach() (chord.ReplicatedStateSourceAttachment[T], error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.closed {
		return nil, errors.New("State source is closed")
	}
	attachment := &sessionSourceAttachment[T]{
		snapshot:  chord.ReplicatedStateSnapshot[T]{Value: source.value, Cursor: source.cursor},
		scheduler: source.scheduler,
	}
	attachment.release = func() {
		source.mu.Lock()
		delete(source.attachments, attachment)
		empty := len(source.attachments) == 0
		source.mu.Unlock()
		if empty {
			source.finishDisposal()
		}
	}
	source.attachments[attachment] = struct{}{}
	return attachment, nil
}

func (source *CommittedStateSource[T]) Advance(ctx context.Context, value T, ops []durable.Op) {
	source.mu.Lock()
	if source.closed || source.retired {
		source.mu.Unlock()
		return
	}
	source.value = value
	source.cursor++
	if isRetiredValue(value) {
		source.retired = true
	}
	frame := chord.ReplicatedStateSourceFrame[T]{Cursor: source.cursor, Value: value, Ops: ops, Context: ctx}
	attachments := source.snapshotAttachments()
	source.mu.Unlock()
	for _, attachment := range attachments {
		attachment.publish(frame)
	}
}

func (source *CommittedStateSource[T]) snapshotAttachments() []*sessionSourceAttachment[T] {
	attachments := make([]*sessionSourceAttachment[T], 0, len(source.attachments))
	for attachment := range source.attachments {
		attachments = append(attachments, attachment)
	}
	return attachments
}

func (source *CommittedStateSource[T]) CloseSession() {
	source.mu.Lock()
	if source.closed {
		source.mu.Unlock()
		return
	}
	attachments := source.snapshotAttachments()
	source.mu.Unlock()
	for _, attachment := range attachments {
		attachment.Dispose()
	}
	source.finishDisposal()
}

func (source *CommittedStateSource[T]) finishDisposal() {
	source.mu.Lock()
	if source.closed {
		source.mu.Unlock()
		return
	}
	source.closed = true
	var zero T
	source.value = zero
	release := source.release
	source.release = nil
	source.mu.Unlock()
	if release != nil {
		release()
	}
}

type sessionSourceAttachment[T any] struct {
	mu         sync.Mutex
	snapshot   chord.ReplicatedStateSnapshot[T]
	scheduler  *pending
	release    func()
	frames     []chord.ReplicatedStateSourceFrame[T]
	listener   func(chord.ReplicatedStateSourceFrame[T])
	activated  bool
	disposed   bool
	scheduled  bool
	delivering bool
}

func (attachment *sessionSourceAttachment[T]) Snapshot() chord.ReplicatedStateSnapshot[T] {
	return attachment.snapshot
}

func (attachment *sessionSourceAttachment[T]) Activate(listener func(chord.ReplicatedStateSourceFrame[T])) error {
	attachment.mu.Lock()
	if attachment.activated {
		attachment.mu.Unlock()
		return errors.New("State attachment is already active")
	}
	if attachment.disposed {
		attachment.mu.Unlock()
		return errors.New("State attachment is disposed")
	}
	attachment.activated = true
	attachment.listener = listener
	attachment.mu.Unlock()
	attachment.drain()
	return nil
}

func (attachment *sessionSourceAttachment[T]) publish(frame chord.ReplicatedStateSourceFrame[T]) {
	attachment.mu.Lock()
	defer attachment.mu.Unlock()
	if attachment.disposed {
		return
	}
	attachment.frames = append(attachment.frames, frame)
	if !attachment.activated || attachment.delivering || attachment.scheduled {
		return
	}
	attachment.scheduled = true
	attachment.scheduler.queueDrain(func() {
		attachment.mu.Lock()
		attachment.scheduled = false
		disposed := attachment.disposed
		attachment.mu.Unlock()
		if !disposed {
			attachment.drainIsolated()
		}
	})
}

// drainIsolated runs a scheduled drain. Chord's frame listener contains source-contract failures; an unexpected
// direct listener failure disposes only this attachment, as upstream's microtask catches it and calls dispose().
func (attachment *sessionSourceAttachment[T]) drainIsolated() {
	defer func() {
		// upstream: packages/durable/src/session/observation.ts:SessionSourceAttachment.publish
		if recover() != nil {
			attachment.Dispose()
		}
	}()
	attachment.drain()
}

func (attachment *sessionSourceAttachment[T]) Dispose() {
	attachment.mu.Lock()
	if attachment.disposed {
		attachment.mu.Unlock()
		return
	}
	attachment.disposed = true
	attachment.frames = nil
	attachment.listener = nil
	release := attachment.release
	attachment.release = nil
	attachment.mu.Unlock()
	if release != nil {
		release()
	}
}

func (attachment *sessionSourceAttachment[T]) drain() {
	attachment.mu.Lock()
	if attachment.listener == nil || attachment.delivering || attachment.disposed {
		attachment.mu.Unlock()
		return
	}
	attachment.delivering = true
	finished := false
	defer func() {
		// A listener panic leaves the lock released; reset delivering as upstream's finally does.
		if !finished {
			attachment.mu.Lock()
			attachment.delivering = false
			attachment.mu.Unlock()
		}
	}()
	for !attachment.disposed && len(attachment.frames) > 0 {
		frame := attachment.frames[0]
		attachment.frames = attachment.frames[1:]
		listener := attachment.listener
		attachment.mu.Unlock()
		listener(frame)
		attachment.mu.Lock()
	}
	attachment.delivering = false
	finished = true
	attachment.mu.Unlock()
}

type watchFrame[T any] struct {
	value T
	ops   []durable.Op
	ctx   context.Context
}

// CommittedWatch is a serialized exact-frame watch bound to one document
// incarnation or conversation view. A nil value retires it.
type CommittedWatch[T any] struct {
	mu        sync.Mutex
	scheduler *pending
	detach    func()
	replace   func() T
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
	return newCommittedWatch(value, detach, replace, session.scheduler)
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

func (watch *CommittedWatch[T]) CloseSession() {
	watch.terminate(durable.WatchEnd{Reason: durable.WatchSessionClosed})
}

func (watch *CommittedWatch[T]) Advance(ctx context.Context, value T, ops []durable.Op) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.end != nil || watch.retired {
		return
	}
	if isRetiredValue(value) {
		watch.retired = true
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

// deliverLocked starts delivery of the oldest pending frame when no callback is in flight. Upstream's commit publishes
// before its promise resolves and the drain microtask that advance queues enters the listener before the commit's
// caller resumes, so the frame is taken into delivery before Advance returns to the publishing commit; a later Stop
// prevents only frames behind it. The listener itself runs on a delivery goroutine, never inline.
func (watch *CommittedWatch[T]) deliverLocked() {
	if watch.running || watch.end != nil || len(watch.pending) == 0 {
		return
	}
	frame := watch.takeLocked()
	watch.scheduler.schedule(func() { watch.drain(frame) })
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
		frame := watch.takeLocked()
		watch.mu.Unlock()
		watch.drain(frame)
	})
}

// takeLocked marks a callback in flight and makes the oldest pending frame the watch value.
func (watch *CommittedWatch[T]) takeLocked() watchFrame[T] {
	watch.running = true
	frame := watch.pending[0]
	watch.pending = watch.pending[1:]
	watch.value = frame.value
	return frame
}

// drain delivers frame, which takeLocked took, then every frame buffered behind it until the watch ends.
func (watch *CommittedWatch[T]) drain(frame watchFrame[T]) {
	watch.mu.Lock()
	for {
		listener := watch.listener
		watch.mu.Unlock()
		err := callWatchListener(listener, frame)
		watch.mu.Lock()
		if err != nil {
			if watch.end == nil {
				watch.terminateLocked(durable.WatchEnd{Reason: durable.WatchListenerError, Error: err})
			}
			break
		}
		if isRetiredValue(frame.value) {
			watch.terminateLocked(durable.WatchEnd{Reason: durable.WatchRetired})
			break
		}
		if watch.end != nil || len(watch.pending) == 0 {
			break
		}
		frame = watch.takeLocked()
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
	if !watch.detached {
		watch.detached = true
		detach := watch.detach
		watch.mu.Unlock()
		detach()
		watch.mu.Lock()
	}
	watch.pending = nil
	watch.finishIfReadyLocked()
}

func (watch *CommittedWatch[T]) finishIfReadyLocked() {
	if watch.resolved || watch.end == nil {
		return
	}
	watch.resolved = true
	if watch.stopAfter != nil {
		watch.stopAfter()
	}
	close(watch.closed)
}
