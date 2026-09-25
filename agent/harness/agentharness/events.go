package agentharness

import (
	"errors"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// This file ports packages/agent/src/harness/events.ts.
//
// Upstream chains deliveries on a promise tail. Go keeps the same FIFO with a
// chain of completion channels: every emitted batch and resnapshot barrier
// takes the current tail and runs after it. Emit and EmitBatch deliver on the
// caller's goroutine once earlier work is done and return when their own batch
// is delivered, like awaiting the upstream promise. A listener must therefore
// not synchronously wait for another Emit, which would wait for itself
// (upstream deadlocks the same way when a listener awaits emit). Barriers and
// watcher deliveries, which upstream never awaits, run on short-lived
// goroutines that exit when their turn completes.

// ResnapshotCapture captures a replacement snapshot and must call
// markBoundary exactly once, at the point in the event order the snapshot
// reflects.
type ResnapshotCapture[T any] func(ctx harness.Context, markBoundary func() error) (T, error)

type listenerEntry struct {
	listener EventListener
}

// HarnessEventBus is the passive harness event bus with isolated handler
// failures (upstream HarnessEventBus). Listener errors become handler_error
// events delivered to handler_error listeners and watchers; failures while
// delivering handler_error are dropped.
type HarnessEventBus struct {
	mu             sync.Mutex
	listeners      map[HarnessEventType][]*listenerEntry
	watchListeners []*listenerEntry
	tail           chan struct{}
	closedError    error
}

var _ Events = (*HarnessEventBus)(nil)

// NewHarnessEventBus creates an open bus with no listeners.
func NewHarnessEventBus() *HarnessEventBus {
	tail := make(chan struct{})
	close(tail)
	return &HarnessEventBus{listeners: map[HarnessEventType][]*listenerEntry{}, tail: tail}
}

// On registers a listener for one event type and returns its unsubscribe
// function. Registering the same function twice registers it twice.
func (bus *HarnessEventBus) On(eventType HarnessEventType, listener EventListener) (func(), error) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if bus.closedError != nil {
		return nil, bus.closedError
	}
	entry := &listenerEntry{listener: listener}
	bus.listeners[eventType] = append(bus.listeners[eventType], entry)
	return func() {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		bus.listeners[eventType] = removeEntry(bus.listeners[eventType], entry)
	}, nil
}

func removeEntry(entries []*listenerEntry, entry *listenerEntry) []*listenerEntry {
	index := slices.Index(entries, entry)
	if index == -1 {
		return entries
	}
	return slices.Delete(slices.Clone(entries), index, index+1)
}

// Emit delivers one event; see EmitBatch.
func (bus *HarnessEventBus) Emit(ctx harness.Context, event HarnessEvent) {
	bus.EmitBatch(ctx, []HarnessEvent{event})
}

// EmitBatch binds the current recipients of each event now, then delivers the
// batch contiguously after all earlier batches. Each recipient receives its
// own deep copy of the event. It returns once the batch is delivered. A closed
// bus or an empty batch delivers nothing.
func (bus *HarnessEventBus) EmitBatch(ctx harness.Context, events []HarnessEvent) {
	bus.mu.Lock()
	if bus.closedError != nil || len(events) == 0 {
		bus.mu.Unlock()
		return
	}
	type boundEvent struct {
		payload    HarnessEvent
		recipients []*listenerEntry
	}
	bound := make([]boundEvent, 0, len(events))
	for _, event := range events {
		payload := CloneHarnessEvent(event)
		bound = append(bound, boundEvent{payload: payload, recipients: bus.snapshotRecipientsLocked(payload)})
	}
	previous, done := bus.tail, make(chan struct{})
	bus.tail = done
	bus.mu.Unlock()

	defer close(done)
	<-previous
	for _, item := range bound {
		bus.deliver(ctx, item.payload, item.recipients, true)
	}
}

// Watch installs a buffering watcher over events matching filter, starting
// from snapshot. resnapshot may be nil, in which case Resnapshot fails.
func Watch[T any](bus *HarnessEventBus, snapshot T, filter func(HarnessEvent) bool, resnapshot ResnapshotCapture[T]) (*BufferedEventWatcher[T], error) {
	if err := bus.closed(); err != nil {
		return nil, err
	}
	return installWatcher(bus, snapshot, filter, resnapshot), nil
}

// WatchFromSnapshot installs a watcher, then captures its initial snapshot.
// Events emitted during the capture are buffered. Resnapshot reuses capture
// and marks the boundary when capture returns. A failed capture unsubscribes
// the watcher and returns the error.
func WatchFromSnapshot[T any](ctx harness.Context, bus *HarnessEventBus, capture func(ctx harness.Context) (T, error), filter func(HarnessEvent) bool) (*BufferedEventWatcher[T], error) {
	if err := bus.closed(); err != nil {
		return nil, err
	}
	var zero T
	watcher := installWatcher(bus, zero, filter, func(captureContext harness.Context, markBoundary func() error) (T, error) {
		snapshot, err := capture(captureContext)
		if err != nil {
			return snapshot, err
		}
		if err := markBoundary(); err != nil {
			return snapshot, err
		}
		return snapshot, nil
	})
	snapshot, err := capture(ctx)
	if err != nil {
		watcher.Unsubscribe()
		return nil, err
	}
	watcher.setSnapshot(snapshot)
	return watcher, nil
}

// Close refuses later registrations and emits with the first supplied error.
// Batches already bound are delivered, including any handler_error reports, before registrations are cleared.
func (bus *HarnessEventBus) Close(err error) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if bus.closedError != nil {
		return
	}
	bus.closedError = err
	bus.enqueueBarrierLocked(func() {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		bus.listeners = map[HarnessEventType][]*listenerEntry{}
		bus.watchListeners = nil
	})
}

func (bus *HarnessEventBus) closed() error {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	return bus.closedError
}

func (bus *HarnessEventBus) snapshotRecipientsLocked(event HarnessEvent) []*listenerEntry {
	recipients := slices.Clone(bus.listeners[event.Type()])
	return append(recipients, bus.watchListeners...)
}

func (bus *HarnessEventBus) snapshotRecipients(event HarnessEvent) []*listenerEntry {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	return bus.snapshotRecipientsLocked(event)
}

// enqueueBarrier runs barrier after all earlier deliveries without waiting.
func (bus *HarnessEventBus) enqueueBarrier(barrier func()) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	bus.enqueueBarrierLocked(barrier)
}

func (bus *HarnessEventBus) enqueueBarrierLocked(barrier func()) {
	previous, done := bus.tail, make(chan struct{})
	bus.tail = done
	go func() {
		defer close(done)
		<-previous
		barrier()
	}()
}

func (bus *HarnessEventBus) deliver(ctx harness.Context, event HarnessEvent, recipients []*listenerEntry, reportErrors bool) {
	for _, recipient := range recipients {
		err := callListener(recipient.listener, ctx, CloneHarnessEvent(event))
		if err == nil || !reportErrors || event.Type() == EventHandlerError {
			continue
		}
		handlerError := eventHandlerError(event, err)
		bus.deliver(ctx, handlerError, bus.snapshotRecipients(handlerError), false)
	}
}

// eventHandlerError builds the handler_error event for a failed event
// listener, carrying the failed event's lane when it has one.
func eventHandlerError(event HarnessEvent, err error) HarnessEvent {
	return HarnessEvent{
		Payload: HandlerErrorPayload{Kind: "event", Event: string(event.Type()), Error: err.Error()},
		Lane:    event.Lane,
	}
}

func installWatcher[T any](bus *HarnessEventBus, snapshot T, filter func(HarnessEvent) bool, resnapshot ResnapshotCapture[T]) *BufferedEventWatcher[T] {
	watcher := &BufferedEventWatcher[T]{snapshot: snapshot, state: watcherBuffering}
	idle := make(chan struct{})
	close(idle)
	watcher.tail = idle
	if resnapshot != nil {
		watcher.resnapshotCallback = func(ctx harness.Context) (T, error) {
			var markMu sync.Mutex
			marked := false
			next, err := resnapshot(ctx, func() error {
				markMu.Lock()
				defer markMu.Unlock()
				if marked {
					return errors.New("Resnapshot boundary was already marked")
				}
				marked = true
				bus.enqueueBarrier(watcher.markResnapshotBoundary)
				return nil
			})
			if err != nil {
				return next, err
			}
			markMu.Lock()
			defer markMu.Unlock()
			if !marked {
				return next, errors.New("Resnapshot capture did not mark its boundary")
			}
			return next, nil
		}
	}
	watcher.onError = func(ctx harness.Context, err error, event HarnessEvent) {
		if event.Type() == EventHandlerError {
			return
		}
		bus.Emit(ctx, eventHandlerError(event, err))
	}
	entry := &listenerEntry{listener: func(ctx harness.Context, event HarnessEvent) error {
		if filter(event) {
			watcher.push(ctx, event)
		}
		return nil
	}}
	bus.mu.Lock()
	bus.watchListeners = append(bus.watchListeners, entry)
	bus.mu.Unlock()
	watcher.unsubscribeCallback = func() {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		bus.watchListeners = removeEntry(bus.watchListeners, entry)
	}
	return watcher
}

type watcherState int

const (
	watcherBuffering watcherState = iota
	watcherStarted
	watcherUnsubscribed
)

type resnapshotPhase int

const (
	resnapshotDropping resnapshotPhase = iota
	resnapshotHolding
)

type watchedEvent struct {
	ctx   harness.Context
	event HarnessEvent
	epoch int
}

type resnapshotState struct {
	phase   resnapshotPhase
	held    []watchedEvent
	reached chan struct{}
}

// BufferedEventWatcher is a WatchHandle that buffers events until Start and
// supports boundary-aligned resnapshots (upstream BufferedEventWatcher).
type BufferedEventWatcher[T any] struct {
	mu                  sync.Mutex
	snapshot            T
	resnapshotCallback  func(ctx harness.Context) (T, error)
	onError             func(ctx harness.Context, err error, event HarnessEvent)
	buffer              []watchedEvent
	listener            EventListener
	unsubscribeCallback func()
	tail                chan struct{}
	epoch               int
	resnapshot          *resnapshotState
	state               watcherState
}

var _ WatchHandle[int] = (*BufferedEventWatcher[int])(nil)

// Snapshot returns the current snapshot.
func (watcher *BufferedEventWatcher[T]) Snapshot() T {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	return watcher.snapshot
}

func (watcher *BufferedEventWatcher[T]) setSnapshot(snapshot T) {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	watcher.snapshot = snapshot
}

// Start delivers buffered events, then live events, to listener in order. It
// may be called only once and not after Unsubscribe.
func (watcher *BufferedEventWatcher[T]) Start(listener EventListener) error {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if watcher.state != watcherBuffering {
		return errors.New("WatchHandle.start() may be called only once")
	}
	watcher.state = watcherStarted
	watcher.listener = listener
	buffered := watcher.buffer
	watcher.buffer = nil
	for _, item := range buffered {
		watcher.enqueueLocked(item)
	}
	return nil
}

// Resnapshot captures a replacement snapshot. Events are dropped until the
// capture's boundary is reached in the bus delivery order, held from the
// boundary until the capture completes, then delivered after the new
// snapshot. Queued events of the previous epoch are not delivered.
func (watcher *BufferedEventWatcher[T]) Resnapshot(ctx harness.Context) (T, error) {
	var zero T
	watcher.mu.Lock()
	switch {
	case watcher.state == watcherUnsubscribed:
		watcher.mu.Unlock()
		return zero, errors.New("WatchHandle is unsubscribed")
	case watcher.resnapshotCallback == nil:
		watcher.mu.Unlock()
		return zero, errors.New("WatchHandle does not support resnapshot")
	case watcher.resnapshot != nil:
		watcher.mu.Unlock()
		return zero, errors.New("WatchHandle resnapshot is already in progress")
	}
	state := &resnapshotState{phase: resnapshotDropping, reached: make(chan struct{})}
	watcher.epoch++
	watcher.resnapshot = state
	callback := watcher.resnapshotCallback
	watcher.mu.Unlock()

	snapshot, err := callback(ctx)
	if err == nil {
		<-state.reached
	}
	watcher.mu.Lock()
	if err == nil {
		watcher.snapshot = snapshot
	}
	watcher.resnapshot = nil
	held := state.held
	for _, item := range held {
		watcher.pushLocked(item.ctx, item.event)
	}
	watcher.mu.Unlock()
	if err != nil {
		return zero, err
	}
	return snapshot, nil
}

func (watcher *BufferedEventWatcher[T]) markResnapshotBoundary() {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	state := watcher.resnapshot
	if state == nil || state.phase != resnapshotDropping {
		return
	}
	state.phase = resnapshotHolding
	close(state.reached)
}

// Unsubscribe stops delivery and removes the watcher from the bus. It is
// idempotent.
func (watcher *BufferedEventWatcher[T]) Unsubscribe() {
	watcher.mu.Lock()
	if watcher.state == watcherUnsubscribed {
		watcher.mu.Unlock()
		return
	}
	watcher.state = watcherUnsubscribed
	watcher.buffer = nil
	watcher.listener = nil
	callback := watcher.unsubscribeCallback
	watcher.unsubscribeCallback = nil
	watcher.mu.Unlock()
	if callback != nil {
		callback()
	}
}

func (watcher *BufferedEventWatcher[T]) push(ctx harness.Context, event HarnessEvent) {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	watcher.pushLocked(ctx, event)
}

func (watcher *BufferedEventWatcher[T]) pushLocked(ctx harness.Context, event HarnessEvent) {
	if watcher.state == watcherUnsubscribed {
		return
	}
	if state := watcher.resnapshot; state != nil {
		if state.phase == resnapshotDropping {
			return
		}
		state.held = append(state.held, watchedEvent{ctx: ctx, event: event})
		return
	}
	item := watchedEvent{ctx: ctx, event: event, epoch: watcher.epoch}
	if watcher.state == watcherBuffering {
		watcher.buffer = append(watcher.buffer, item)
		return
	}
	watcher.enqueueLocked(item)
}

func (watcher *BufferedEventWatcher[T]) enqueueLocked(item watchedEvent) {
	listener := watcher.listener
	if listener == nil {
		return
	}
	previous, done := watcher.tail, make(chan struct{})
	watcher.tail = done
	go func() {
		defer close(done)
		<-previous
		watcher.mu.Lock()
		current := watcher.state == watcherStarted && item.epoch == watcher.epoch
		watcher.mu.Unlock()
		if !current {
			return
		}
		if err := callListener(listener, item.ctx, item.event); err != nil {
			watcher.onError(item.ctx, err, item.event)
		}
	}()
}
