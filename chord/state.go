// Package chord ports the parts of Pi's @earendil-works/chord application
// runtime that the durable packages consume: strict JSON copies and the
// publication-only replicated state attached to an authoritative source.
//
// Chord's Context is context.Context. Listener deliveries that upstream runs
// from the microtask queue run on the goroutine that publishes the source
// frame; callers own any further scheduling.
package chord

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/services/state.ts (ReplicatedStatePublisher,
// StateSubscriber, AttachedReplicatedStateImpl, attachReplicatedStateSource),
// services/state-internals.ts, and the replicated-state declarations of
// packages/chord/src/types.ts.

// DeliveryKind is "hydrate" or "update".
type DeliveryKind string

const (
	DeliveryHydrate DeliveryKind = "hydrate"
	DeliveryUpdate  DeliveryKind = "update"
)

// ReplicatedStateDelivery describes one listener delivery.
type ReplicatedStateDelivery struct {
	Kind     DeliveryKind
	Sequence int
}

// ReplicatedStateSourceFrame is one committed frame of an authoritative source.
type ReplicatedStateSourceFrame[T any] struct {
	// Cursor is monotonic; the first frame after a snapshot is snapshot cursor + 1.
	Cursor int
	// Value is the exact immutable value produced by this commit.
	Value T
	// Ops is the exact immutable batch that produced Value from the preceding revision.
	Ops     []delta.Op
	Context context.Context
}

// ReplicatedStateSnapshot is the fixed immutable snapshot of an attachment.
type ReplicatedStateSnapshot[T any] struct {
	Value  T
	Cursor int
}

// ReplicatedStateSourceAttachment is one source subscription.
type ReplicatedStateSourceAttachment[T any] interface {
	// Snapshot is captured at the atomic attachment boundary.
	Snapshot() ReplicatedStateSnapshot[T]
	// Activate installs the sole listener and drains every buffered frame in source commit order. Single use.
	Activate(listener func(frame ReplicatedStateSourceFrame[T])) error
	// Dispose stops delivery and releases source resources; idempotent.
	Dispose()
}

// ReplicatedStateSource is an authoritative immutable revision source.
type ReplicatedStateSource[T any] interface {
	Attach() (ReplicatedStateSourceAttachment[T], error)
}

// ReplicatedStateSourceOptions configures an attached state.
type ReplicatedStateSourceOptions struct {
	// OnError receives source-contract and listener failures; nil discards them after logging is impossible, matching
	// upstream's asynchronous rethrow, which the host reports as an uncaught error.
	OnError func(error)
}

// StateListener receives one delivery.
type StateListener[T any] func(value T, ctx context.Context, delivery ReplicatedStateDelivery)

// SourceListener receives one published operation batch.
type SourceListener func(ops []delta.Op, sequence int, ctx context.Context)

// ReplicatedStateInternals exposes a state's publication stream (upstream getReplicatedStateInternals).
type ReplicatedStateInternals[T any] interface {
	// InternalSnapshot atomically captures the immutable value and its publication sequence.
	InternalSnapshot() (T, int)
	// SubscribeSource observes every published operation batch.
	SubscribeSource(listener SourceListener) func()
}

type stateFrame[T any] struct {
	value    T
	ctx      context.Context
	delivery ReplicatedStateDelivery
}

// stateSubscriber is one public subscription. Listeners are synchronous, so
// at most the frame being delivered is pending; the upstream 100-frame
// overflow window cannot fill.
type stateSubscriber[T any] struct {
	listener StateListener[T]
	report   func(error)
	pending  []stateFrame[T]
	running  bool
	closed   atomic.Bool
}

func (subscriber *stateSubscriber[T]) push(frame stateFrame[T]) {
	if subscriber.closed.Load() {
		return
	}
	subscriber.pending = append(subscriber.pending, frame)
}

func (subscriber *stateSubscriber[T]) drain() {
	if subscriber.running || subscriber.closed.Load() {
		return
	}
	subscriber.running = true
	defer func() { subscriber.running = false }()
	for len(subscriber.pending) > 0 && !subscriber.closed.Load() {
		frame := subscriber.pending[0]
		subscriber.pending = subscriber.pending[1:]
		if err := callStateListener(subscriber.listener, frame); err != nil {
			subscriber.report(err)
		}
	}
}

// callStateListener isolates a panicking listener as upstream isolates a throwing one.
func callStateListener[T any](listener StateListener[T], frame stateFrame[T]) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicError(recovered)
		}
	}()
	listener(frame.value, frame.ctx, frame.delivery)
	return nil
}

func callSourceListener(listener SourceListener, ops []delta.Op, sequence int, ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicError(recovered)
		}
	}()
	listener(ops, sequence, ctx)
	return nil
}

func panicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("%v", recovered)
}

// statePublisher maintains local publication order. deliveryMu serializes
// publications and hydrations, so a listener must not subscribe to or publish
// on the same state; mu guards the fields and is never held across a listener.
type statePublisher[T any] struct {
	deliveryMu      sync.Mutex
	mu              sync.Mutex
	listeners       map[*stateSubscriber[T]]int
	order           []*stateSubscriber[T]
	sourceListeners map[*SourceListener]struct{}
	sourceOrder     []*SourceListener
	report          func(error)
	value           T
	sequence        int
}

func newStatePublisher[T any](initial T, report func(error)) *statePublisher[T] {
	return &statePublisher[T]{
		listeners:       map[*stateSubscriber[T]]int{},
		sourceListeners: map[*SourceListener]struct{}{},
		report:          report,
		value:           initial,
	}
}

func (publisher *statePublisher[T]) snapshot() (T, int) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	return publisher.value, publisher.sequence
}

func (publisher *statePublisher[T]) subscribe(listener StateListener[T]) func() {
	publisher.deliveryMu.Lock()
	defer publisher.deliveryMu.Unlock()
	publisher.mu.Lock()
	subscriber := &stateSubscriber[T]{listener: listener, report: publisher.report}
	publisher.listeners[subscriber] = publisher.sequence
	publisher.order = append(publisher.order, subscriber)
	frame := stateFrame[T]{
		value:    publisher.value,
		ctx:      context.Background(),
		delivery: ReplicatedStateDelivery{Kind: DeliveryHydrate, Sequence: publisher.sequence},
	}
	publisher.mu.Unlock()
	subscriber.push(frame)
	subscriber.drain()
	return func() {
		subscriber.closed.Store(true)
		publisher.mu.Lock()
		defer publisher.mu.Unlock()
		if _, ok := publisher.listeners[subscriber]; ok {
			delete(publisher.listeners, subscriber)
			for index, entry := range publisher.order {
				if entry == subscriber {
					publisher.order = append(publisher.order[:index:index], publisher.order[index+1:]...)
					break
				}
			}
		}
	}
}

func (publisher *statePublisher[T]) subscribeSource(listener SourceListener) func() {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	key := &listener
	publisher.sourceListeners[key] = struct{}{}
	publisher.sourceOrder = append(publisher.sourceOrder, key)
	return func() {
		publisher.mu.Lock()
		defer publisher.mu.Unlock()
		if _, ok := publisher.sourceListeners[key]; !ok {
			return
		}
		delete(publisher.sourceListeners, key)
		for index, entry := range publisher.sourceOrder {
			if entry == key {
				publisher.sourceOrder = append(publisher.sourceOrder[:index:index], publisher.sourceOrder[index+1:]...)
				break
			}
		}
	}
}

// publish publishes an already-prepared immutable revision and returns the
// isolated listener failures.
func (publisher *statePublisher[T]) publish(value T, ops []delta.Op, ctx context.Context) []error {
	publisher.deliveryMu.Lock()
	defer publisher.deliveryMu.Unlock()
	publisher.mu.Lock()
	publisher.value = value
	publisher.sequence++
	sequence := publisher.sequence
	sources := append([]*SourceListener(nil), publisher.sourceOrder...)
	subscribers := append([]*stateSubscriber[T](nil), publisher.order...)
	hydrated := make([]int, len(subscribers))
	for index, subscriber := range subscribers {
		hydrated[index] = publisher.listeners[subscriber]
	}
	publisher.mu.Unlock()
	var errs []error
	for _, key := range sources {
		if err := callSourceListener(*key, ops, sequence, ctx); err != nil {
			errs = append(errs, err)
		}
	}
	delivery := ReplicatedStateDelivery{Kind: DeliveryUpdate, Sequence: sequence}
	for index, subscriber := range subscribers {
		if sequence <= hydrated[index] {
			continue
		}
		subscriber.push(stateFrame[T]{value: value, ctx: ctx, delivery: delivery})
		subscriber.drain()
	}
	return errs
}

// AttachedReplicatedState is a synchronously hydrated publication-only state
// backed by one source attachment.
type AttachedReplicatedState[T any] struct {
	publisher  *statePublisher[T]
	attachment ReplicatedStateSourceAttachment[T]
	onError    func(error)
	mu         sync.Mutex
	cursor     int
	disposed   bool
}

// Value returns the latest published immutable value; it stays readable after Dispose.
func (state *AttachedReplicatedState[T]) Value() T {
	value, _ := state.publisher.snapshot()
	return value
}

// Subscribe delivers the current value as a hydration, then every later update.
func (state *AttachedReplicatedState[T]) Subscribe(listener func(T, context.Context, ReplicatedStateDelivery)) (func(), error) {
	return state.publisher.subscribe(listener), nil
}

// InternalSnapshot implements ReplicatedStateInternals.
func (state *AttachedReplicatedState[T]) InternalSnapshot() (T, int) {
	return state.publisher.snapshot()
}

// SubscribeSource implements ReplicatedStateInternals.
func (state *AttachedReplicatedState[T]) SubscribeSource(listener SourceListener) func() {
	return state.publisher.subscribeSource(listener)
}

// Dispose idempotently releases the source attachment.
func (state *AttachedReplicatedState[T]) Dispose() {
	state.mu.Lock()
	if state.disposed {
		state.mu.Unlock()
		return
	}
	state.disposed = true
	state.mu.Unlock()
	state.attachment.Dispose()
}

func (state *AttachedReplicatedState[T]) receive(frame ReplicatedStateSourceFrame[T]) {
	state.mu.Lock()
	if state.disposed {
		state.mu.Unlock()
		return
	}
	expected := state.cursor + 1
	if frame.Cursor != expected {
		state.mu.Unlock()
		state.fail(fmt.Errorf("Replicated state source cursor has a gap: expected %d, received %d", expected, frame.Cursor))
		return
	}
	state.cursor = frame.Cursor
	state.mu.Unlock()
	errs := state.publisher.publish(frame.Value, frame.Ops, frame.Context)
	switch len(errs) {
	case 0:
	case 1:
		state.report(errs[0])
	default:
		state.report(fmt.Errorf("Replicated state listeners failed: %w", errors.Join(errs...)))
	}
}

func (state *AttachedReplicatedState[T]) fail(err error) {
	state.mu.Lock()
	if state.disposed {
		state.mu.Unlock()
		return
	}
	state.disposed = true
	state.mu.Unlock()
	state.attachment.Dispose()
	state.report(err)
}

func (state *AttachedReplicatedState[T]) report(err error) {
	if state.onError != nil {
		state.onError(err)
	}
}

// AttachReplicatedStateSource attaches a publication-only replicated state to
// one authoritative immutable source stream.
func AttachReplicatedStateSource[T any](source ReplicatedStateSource[T], options ReplicatedStateSourceOptions) (*AttachedReplicatedState[T], error) {
	attachment, err := source.Attach()
	if err != nil {
		return nil, err
	}
	snapshot := attachment.Snapshot()
	state := &AttachedReplicatedState[T]{attachment: attachment, onError: options.OnError, cursor: snapshot.Cursor}
	state.publisher = newStatePublisher(snapshot.Value, state.report)
	if err := attachment.Activate(state.receive); err != nil {
		attachment.Dispose()
		return nil, err
	}
	return state, nil
}
