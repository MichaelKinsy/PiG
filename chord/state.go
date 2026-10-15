// Package chord ports Pi's @earendil-works/chord root export base: strict JSON copies, the shared Context (a
// context.Context, see chord/context), and replicated state, either authoritative and mutable (NewReplicatedState) or a
// publication-only view of an external revision source (AttachReplicatedStateSource).
//
// Upstream Chord runs on one JavaScript event loop. A synchronous listener runs inline on the publishing goroutine; a
// listener that returns a Completion (the Go form of a Promise) suspends its own subscription until the completion
// settles, without blocking the producer. Publications made while a delivery runs, on the same goroutine or another,
// are delivered by the running loop in sequence order.
package chord

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// Ports packages/chord/src/state.ts (StateSubscriber, ReplicatedStatePublisher, MutableReplicatedStateImpl,
// AttachedReplicatedStateImpl, attachReplicatedStateSource, replicatedState) and its declarations.

// DeliveryKind is "hydrate" or "update".
type DeliveryKind string

const (
	DeliveryHydrate DeliveryKind = "hydrate"
	DeliveryUpdate  DeliveryKind = "update"
)

// maxPendingDeliveries is state.ts StateSubscriber.push's window: at most 100 deliveries wait behind the running
// callback.
const maxPendingDeliveries = 100

// ReplicatedStateDelivery describes one listener delivery.
type ReplicatedStateDelivery struct {
	Kind     DeliveryKind
	Sequence int
}

// Completion is the Go form of the Promise a listener may return. A listener that has finished returns nil. A listener
// that is still working returns a channel that receives its failure, or nil, exactly once when it settles; its
// subscription delivers nothing else until then.
type Completion <-chan error

// ReplicatedState is a contract-immutable value with ordered, serialized change notifications.
//
// Each subscription serializes callbacks, awaiting hydration before updates. Values are immutable and may share
// unchanged revision data. At most 100 deliveries wait behind the running callback; overflow keeps only the newest
// pending value, context and delivery, so update sequences may skip. Failures are reported in isolation and delivery
// continues. Unsubscribe discards pending work without aborting or joining a callback.
type ReplicatedState[T any] interface {
	// Value is the current immutable value. It is not frozen and may share containers with the provider; callers must
	// not mutate it. Later updates do not mutate previously returned values.
	Value() T
	// Subscribe delivers the current value as a hydration, then every later update.
	Subscribe(listener func(value T, ctx context.Context, delivery ReplicatedStateDelivery)) func()
	// SubscribeAsync is Subscribe for a listener that may still be working when it returns: the subscription waits for
	// the returned Completion before its next delivery.
	SubscribeAsync(listener func(value T, ctx context.Context, delivery ReplicatedStateDelivery) Completion) func()
}

// ReplicatedStateSourceFrame is one immutable authoritative revision committed after an attachment snapshot.
type ReplicatedStateSourceFrame[T any] struct {
	// Cursor is monotonic; the first frame after a snapshot is snapshot cursor + 1.
	Cursor int
	// Value is the exact immutable value produced by this commit.
	Value T
	// Ops is the exact immutable batch that produced Value from the preceding revision. Chord publishes Value as is;
	// it never applies or re-diffs Ops.
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
	// Activate installs the sole listener and synchronously drains every buffered frame in source commit order. It is
	// single use. After it begins, every new committed frame must also be delivered in order until disposal, including
	// commits made reentrantly while a prior frame is being delivered.
	Activate(listener func(frame ReplicatedStateSourceFrame[T])) error
	// Dispose stops delivery and releases source resources; idempotent.
	Dispose()
}

// ReplicatedStateSource is an authoritative immutable revision source. Attach must synchronously and atomically capture
// one snapshot and register the returned attachment to buffer every later committed frame, with no overlap or gap.
type ReplicatedStateSource[T any] interface {
	Attach() (ReplicatedStateSourceAttachment[T], error)
}

// ReplicatedStateSourceOptions configures an attached state.
type ReplicatedStateSourceOptions struct {
	// OnError receives source-contract and publication-listener failures without throwing them into the source. When it
	// is nil, or panics, the failure goes to the uncaught reporter (SetUncaughtErrorReporter).
	OnError func(error)
}

// AggregateError is JavaScript's AggregateError(errors, message): Error() is the message alone and the causes are
// reachable through Unwrap.
type AggregateError struct {
	Message string
	Errors  []error
}

func (err *AggregateError) Error() string   { return err.Message }
func (err *AggregateError) Unwrap() []error { return err.Errors }

// NewAggregateError builds an AggregateError over failures in order.
func NewAggregateError(message string, failures ...error) *AggregateError {
	return &AggregateError{Message: message, Errors: failures}
}

var uncaughtReporter atomic.Pointer[func(error)]

// exitOnUncaught is the default receiver of unhandled failures. state.ts reportErrorAsync rethrows the failure in a
// microtask, so it is thrown after the reporting call and, with no uncaughtException handler, Node prints it to
// standard error and exits with status 1.
var exitOnUncaught = func(err error) {
	fmt.Fprintf(os.Stderr, "%s\n", err)
	os.Exit(1)
}

func init() {
	report := func(err error) {
		//portlint:allow golifetime the goroutine ends the process, as the microtask rethrow of state.ts reportErrorAsync does; nothing outlives it
		go exitOnUncaught(err) //portlint:allow asyncorder reportErrorAsync's microtask rethrow comes after the reporting call and ends the process; nothing is ordered after it
	}
	uncaughtReporter.Store(&report)
}

// SetUncaughtErrorReporter replaces the receiver of failures no error reporter handled: the Go form of state.ts
// reportErrorAsync, which rethrows in a microtask and so reaches the process's uncaught-exception handler. The default
// ends the process as an unhandled throw does, from its own goroutine so the reporting call is not interrupted. It
// returns the previous receiver and is safe to call while states deliver.
func SetUncaughtErrorReporter(report func(error)) (previous func(error)) {
	return *uncaughtReporter.Swap(&report)
}

// reportErrorAsync hands err to the uncaught reporter; it never returns the failure to the publishing call.
func reportErrorAsync(err error) { (*uncaughtReporter.Load())(err) }

// toError is state.ts toError for a recovered panic or a returned failure.
func toError(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return fmt.Errorf("%v", value)
}

type stateDelivery[T any] struct {
	value    T
	ctx      context.Context
	delivery ReplicatedStateDelivery
}

type stateListener[T any] func(value T, ctx context.Context, delivery ReplicatedStateDelivery) Completion

// stateSubscriber is one public subscription, independent of producer and other subscriber progress.
type stateSubscriber[T any] struct {
	listener    stateListener[T]
	reportError func(error)

	mu      sync.Mutex
	pending []stateDelivery[T]
	running bool
	started bool
	closed  bool
}

func (subscriber *stateSubscriber[T]) push(frame stateDelivery[T]) {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	if subscriber.closed {
		return
	}
	if len(subscriber.pending) == maxPendingDeliveries {
		// A cold replica can queue updates reentrantly before this subscriber's first hydration starts.
		var hydration []stateDelivery[T]
		if !subscriber.started {
			hydration = []stateDelivery[T]{subscriber.pending[0]}
		}
		subscriber.pending = hydration
	}
	subscriber.pending = append(subscriber.pending, frame)
}

func (subscriber *stateSubscriber[T]) drain() {
	subscriber.mu.Lock()
	if subscriber.running || subscriber.closed {
		subscriber.mu.Unlock()
		return
	}
	subscriber.running = true
	for len(subscriber.pending) > 0 {
		frame := subscriber.pending[0]
		subscriber.pending = subscriber.pending[1:]
		subscriber.started = true
		subscriber.mu.Unlock()
		completion, err := callStateListener(subscriber.listener, frame)
		if err != nil {
			subscriber.report(err)
		}
		if completion != nil {
			//portlint:allow golifetime state.ts drain resumes from an unawaited then of the listener's promise; the goroutine ends when the completion settles
			go subscriber.await(completion) //portlint:allow asyncorder state.ts drain continues only after the listener's promise settles; later frames wait in pending, in order, until await resumes the drain
			return
		}
		subscriber.mu.Lock()
	}
	subscriber.running = false
	subscriber.mu.Unlock()
}

func (subscriber *stateSubscriber[T]) await(completion Completion) {
	if err := <-completion; err != nil {
		subscriber.report(err)
	}
	subscriber.mu.Lock()
	subscriber.running = false
	subscriber.mu.Unlock()
	subscriber.drain()
}

func (subscriber *stateSubscriber[T]) close() {
	subscriber.mu.Lock()
	subscriber.closed = true
	subscriber.pending = nil
	subscriber.mu.Unlock()
}

func (subscriber *stateSubscriber[T]) report(err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			reportErrorAsync(toError(recovered))
		}
	}()
	subscriber.reportError(err)
}

// callStateListener isolates a panicking listener as state.ts isolates a throwing one.
func callStateListener[T any](listener stateListener[T], frame stateDelivery[T]) (completion Completion, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			completion, err = nil, toError(recovered)
		}
	}()
	return listener(frame.value, frame.ctx, frame.delivery), nil
}

type publication[T any] struct {
	value    T
	ops      []delta.Op
	sequence int
	ctx      context.Context
}

type subscription[T any] struct {
	subscriber *stateSubscriber[T]
	hydrated   int
}

// statePublisher maintains local publication order independently of how revisions are produced. mu guards the fields
// and is never held across a listener.
type statePublisher[T any] struct {
	reportError func(error)

	mu           sync.Mutex
	listeners    []*subscription[T]
	sources      []*PublicationListener[T]
	publications []publication[T]
	value        T
	sequence     int
	delivering   bool
}

func newStatePublisher[T any](initial T, reportError func(error)) *statePublisher[T] {
	if reportError == nil {
		reportError = reportErrorAsync
	}
	return &statePublisher[T]{value: initial, reportError: reportError}
}

func (publisher *statePublisher[T]) current() T {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	return publisher.value
}

func (publisher *statePublisher[T]) subscribe(listener stateListener[T]) func() {
	subscriber := &stateSubscriber[T]{listener: listener, reportError: publisher.reportError}
	publisher.mu.Lock()
	entry := &subscription[T]{subscriber: subscriber, hydrated: publisher.sequence}
	publisher.listeners = append(publisher.listeners, entry)
	// Hydration has no caller; it is delivered with the background context.
	subscriber.push(stateDelivery[T]{
		value:    publisher.value,
		ctx:      chordctx.Background(),
		delivery: ReplicatedStateDelivery{Kind: DeliveryHydrate, Sequence: publisher.sequence},
	})
	publisher.mu.Unlock()
	subscriber.drain()
	return func() {
		subscriber.close()
		publisher.mu.Lock()
		defer publisher.mu.Unlock()
		for index, candidate := range publisher.listeners {
			if candidate == entry {
				publisher.listeners = append(publisher.listeners[:index:index], publisher.listeners[index+1:]...)
				return
			}
		}
	}
}

// publish publishes an already-prepared immutable revision. Subscriber failures are reported, never returned.
func (publisher *statePublisher[T]) publish(value T, ops []delta.Op, ctx context.Context) {
	publisher.enqueue(value, ops, ctx)
	if errs := publisher.deliver(); len(errs) == 1 {
		publisher.reportError(errs[0])
	} else if len(errs) > 1 {
		publisher.reportError(NewAggregateError("Replicated state listeners failed", errs...))
	}
}

// enqueue commits value as the next revision in publication order; deliver publishes it.
func (publisher *statePublisher[T]) enqueue(value T, ops []delta.Op, ctx context.Context) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	publisher.value = value
	publisher.sequence++
	publisher.publications = append(publisher.publications, publication[T]{value: value, ops: ops, sequence: publisher.sequence, ctx: ctx})
}

// deliver delivers every enqueued publication in sequence order and returns the source listeners' failures. A
// publication made while another is being delivered, on this call stack or another goroutine, is delivered by the
// running loop, after it.
func (publisher *statePublisher[T]) deliver() []error {
	publisher.mu.Lock()
	if publisher.delivering {
		publisher.mu.Unlock()
		return nil
	}
	publisher.delivering = true
	var errs []error
	for len(publisher.publications) > 0 {
		next := publisher.publications[0]
		publisher.publications = publisher.publications[1:]
		listeners := append([]*subscription[T](nil), publisher.listeners...)
		sources := append([]*PublicationListener[T](nil), publisher.sources...)
		publisher.mu.Unlock()
		for _, source := range sources {
			if err := callSourceListener(*source, next); err != nil {
				errs = append(errs, err)
			}
		}
		delivery := ReplicatedStateDelivery{Kind: DeliveryUpdate, Sequence: next.sequence}
		for _, entry := range listeners {
			if next.sequence <= entry.hydrated {
				continue
			}
			entry.subscriber.push(stateDelivery[T]{value: next.value, ctx: next.ctx, delivery: delivery})
			entry.subscriber.drain()
		}
		publisher.mu.Lock()
	}
	publisher.delivering = false
	publisher.mu.Unlock()
	return errs
}

// SourceListener receives every operation batch an attached state publishes, in sequence order.
type SourceListener func(ops []delta.Op, sequence int, ctx context.Context)

// PublicationListener receives every publication of an attached state, in sequence order, with no gap: its value,
// the operations that produced it, its sequence and context.
type PublicationListener[T any] func(value T, ops []delta.Op, sequence int, ctx context.Context)

func callSourceListener[T any](listener PublicationListener[T], next publication[T]) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = toError(recovered)
		}
	}()
	listener(next.value, next.ops, next.sequence, next.ctx)
	return nil
}

func (publisher *statePublisher[T]) subscribeSource(listener PublicationListener[T]) func() {
	key := &listener
	publisher.mu.Lock()
	publisher.sources = append(publisher.sources, key)
	publisher.mu.Unlock()
	return func() {
		publisher.mu.Lock()
		defer publisher.mu.Unlock()
		for index, candidate := range publisher.sources {
			if candidate == key {
				publisher.sources = append(publisher.sources[:index:index], publisher.sources[index+1:]...)
				return
			}
		}
	}
}

func (publisher *statePublisher[T]) snapshot() (T, int) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	return publisher.value, publisher.sequence
}

func syncListener[T any](listener func(T, context.Context, ReplicatedStateDelivery)) stateListener[T] {
	return func(value T, ctx context.Context, delivery ReplicatedStateDelivery) Completion {
		listener(value, ctx, delivery)
		return nil
	}
}

// JsonRoot is the root of an authoritative replicated state: state.ts `T extends object`, an alias-free strict-JSON
// object or array.
type JsonRoot interface {
	*delta.JsonObject | []any
}

// MutableReplicatedState is authoritative replicated state: replicatedState(initial). It is safe for concurrent use:
// changes from other goroutines serialize; a change or replace from inside a change callback is rejected.
//
// Listener failures never reach Change or Replace: each is reported to the uncaught reporter
// (SetUncaughtErrorReporter) after the revision has been committed, and delivery to the other subscribers continues.
type MutableReplicatedState[T JsonRoot] struct {
	publisher *statePublisher[T]

	changeMu sync.Mutex
	tracker  *delta.Tracker
	// changing is the goroutine running a change callback, or zero.
	changing atomic.Uint64
}

var _ ReplicatedState[*delta.JsonObject] = (*MutableReplicatedState[*delta.JsonObject])(nil)

// NewReplicatedState creates authoritative state by taking immutable ownership of an alias-free strict-JSON root. The
// caller must not mutate initial after this call.
func NewReplicatedState[T JsonRoot](initial T) *MutableReplicatedState[T] {
	var tracker *delta.Tracker
	switch root := any(initial).(type) {
	case *delta.JsonObject:
		tracker = delta.Track(root)
	case []any:
		tracker = delta.TrackArray(root)
	}
	return &MutableReplicatedState[T]{tracker: tracker, publisher: newStatePublisher(initial, reportErrorAsync)}
}

// Value is the current immutable root.
func (state *MutableReplicatedState[T]) Value() T { return state.publisher.current() }

var (
	errChangeReentrant  = errors.New("Replicated state cannot be changed reentrantly from a change callback")
	errReplaceReentrant = errors.New("Replicated state cannot be replaced from a change callback")
	errNotObjectRoot    = errors.New("Replicated state change needs an object root")
	errNotArrayRoot     = errors.New("Replicated state array change needs an array root")
)

// Change atomically publishes one synchronous overlay mutation of an object root. Draft handles are unusable after the
// callback returns. Values placed through the draft are cloned by value. A mutate error or panic aborts the draft and
// is returned; an unchanged draft publishes nothing.
func (state *MutableReplicatedState[T]) Change(ctx context.Context, mutate func(draft *delta.Object) error) error {
	return state.change(ctx, func(change *delta.Change) error {
		draft := change.State()
		if draft == nil {
			return errNotObjectRoot
		}
		return mutate(draft)
	})
}

// ChangeArray is Change for an array root: mutate edits the draft of the root array.
func (state *MutableReplicatedState[T]) ChangeArray(ctx context.Context, mutate func(draft *delta.Array) error) error {
	return state.change(ctx, func(change *delta.Change) error {
		draft := change.Elements()
		if draft == nil {
			return errNotArrayRoot
		}
		return mutate(draft)
	})
}

func (state *MutableReplicatedState[T]) change(ctx context.Context, run func(*delta.Change) error) error {
	self := goroutineid.Current()
	if state.changing.Load() == self {
		return errChangeReentrant
	}
	state.changeMu.Lock()
	prepared, err := state.prepareChange(self, run)
	if err != nil {
		state.changeMu.Unlock()
		return err
	}
	return state.adopt(ctx, prepared)
}

func (state *MutableReplicatedState[T]) prepareChange(self uint64, run func(*delta.Change) error) (prepared *delta.Prepared, err error) {
	change := state.tracker.BeginChange()
	state.changing.Store(self)
	defer func() {
		state.changing.Store(0)
		if recovered := recover(); recovered != nil {
			err = toError(recovered)
		}
		if err != nil {
			change.Abort()
		}
	}()
	if err := run(change); err != nil {
		return nil, err
	}
	return change.Prepare()
}

// Replace atomically takes immutable ownership of an alias-free strict-JSON replacement. The caller must not mutate the
// transferred root after this call. A deeply equal replacement publishes nothing.
func (state *MutableReplicatedState[T]) Replace(ctx context.Context, value T) error {
	if state.changing.Load() == goroutineid.Current() {
		return errReplaceReentrant
	}
	state.changeMu.Lock()
	prepared, err := state.tracker.PrepareReplaceRoot(any(value))
	if err != nil {
		state.changeMu.Unlock()
		return err
	}
	return state.adopt(ctx, prepared)
}

// adopt is entered with changeMu held and returns with it released, before delivery.
func (state *MutableReplicatedState[T]) adopt(ctx context.Context, prepared *delta.Prepared) error {
	if err := state.tracker.Adopt(prepared); err != nil {
		state.changeMu.Unlock()
		return err
	}
	if len(prepared.Ops()) == 0 {
		state.changeMu.Unlock()
		return nil
	}
	value, _ := prepared.ValueRoot().(T)
	// The revision is committed in publication order before the lock is released, so concurrent changes publish in
	// adoption order; delivery runs outside the lock.
	state.publisher.enqueue(value, nil, ctx)
	state.changeMu.Unlock()
	state.publisher.deliver()
	return nil
}

// Subscribe implements ReplicatedState.
func (state *MutableReplicatedState[T]) Subscribe(listener func(T, context.Context, ReplicatedStateDelivery)) func() {
	return state.publisher.subscribe(syncListener(listener))
}

// SubscribeAsync implements ReplicatedState.
func (state *MutableReplicatedState[T]) SubscribeAsync(listener func(T, context.Context, ReplicatedStateDelivery) Completion) func() {
	return state.publisher.subscribe(listener)
}

// AttachedReplicatedState is a synchronously hydrated publication-only state backed by one source attachment.
type AttachedReplicatedState[T any] struct {
	publisher   *statePublisher[T]
	attachment  ReplicatedStateSourceAttachment[T]
	reportError func(error)

	mu       sync.Mutex
	cursor   int
	disposed bool
}

var _ ReplicatedState[int] = (*AttachedReplicatedState[int])(nil)

// Value returns the latest published immutable value; it stays readable after Dispose.
func (state *AttachedReplicatedState[T]) Value() T { return state.publisher.current() }

// Subscribe implements ReplicatedState.
func (state *AttachedReplicatedState[T]) Subscribe(listener func(T, context.Context, ReplicatedStateDelivery)) func() {
	return state.publisher.subscribe(syncListener(listener))
}

// SubscribeAsync implements ReplicatedState.
func (state *AttachedReplicatedState[T]) SubscribeAsync(listener func(T, context.Context, ReplicatedStateDelivery) Completion) func() {
	return state.publisher.subscribe(listener)
}

// InternalSnapshot atomically captures the immutable value and its publication sequence.
//
// InternalSnapshot, SubscribeSource and SubscribePublications are the gap-free observation of an attached state. The
// typed durable document state (durable/documents.go) re-attaches each publication decoded as its Go type and needs
// every cursor, which the public subscription's 100-delivery window does not give. Upstream Chord's root export
// has no exact-operation subscription (a TypeScript typed state is the erased state), so a mutable state has none and
// its listener failures are always reported asynchronously.
func (state *AttachedReplicatedState[T]) InternalSnapshot() (T, int) {
	return state.publisher.snapshot()
}

// SubscribeSource observes every operation batch the state publishes, before its subscribers; failures are reported
// through the state's error reporter.
func (state *AttachedReplicatedState[T]) SubscribeSource(listener SourceListener) func() {
	return state.publisher.subscribeSource(func(_ T, ops []delta.Op, sequence int, ctx context.Context) {
		listener(ops, sequence, ctx)
	})
}

// SubscribePublications observes every publication with its value, before the state's subscribers. A publication
// whose sequence is at most InternalSnapshot's, read after subscribing, is already in that snapshot. Failures are
// reported through the state's error reporter.
func (state *AttachedReplicatedState[T]) SubscribePublications(listener PublicationListener[T]) func() {
	return state.publisher.subscribeSource(listener)
}

// Dispose idempotently releases the source attachment. The last published value remains readable.
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
	if err := assertCursor(frame.Cursor, "frame"); err != nil {
		state.mu.Unlock()
		state.fail(err)
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
	state.publisher.publish(frame.Value, frame.Ops, frame.Context)
}

func (state *AttachedReplicatedState[T]) fail(err error) {
	state.mu.Lock()
	if state.disposed {
		state.mu.Unlock()
		return
	}
	state.disposed = true
	state.mu.Unlock()
	if disposeErr := disposeAttachment(state.attachment); disposeErr != nil {
		state.report(NewAggregateError("Replicated state source contract failed", err, disposeErr))
		return
	}
	state.report(err)
}

func (state *AttachedReplicatedState[T]) report(err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			reportErrorAsync(toError(recovered))
		}
	}()
	state.reportError(err)
}

// disposeAttachment disposes attachment and returns its panic as an error, as state.ts catches a throwing dispose.
func disposeAttachment[T any](attachment ReplicatedStateSourceAttachment[T]) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = toError(recovered)
		}
	}()
	attachment.Dispose()
	return nil
}

// AttachReplicatedStateSource attaches a publication-only replicated state to one authoritative immutable source
// stream: replicatedState(source, options). An attachment that fails to activate is disposed; when that dispose also
// fails, both failures return as an AggregateError.
func AttachReplicatedStateSource[T any](source ReplicatedStateSource[T], options ReplicatedStateSourceOptions) (*AttachedReplicatedState[T], error) {
	attachment, err := source.Attach()
	if err != nil {
		return nil, err
	}
	state, err := activateAttachment(attachment, options)
	if err != nil {
		if disposeErr := disposeAttachment(attachment); disposeErr != nil {
			return nil, NewAggregateError("Failed to attach replicated state source", err, disposeErr)
		}
		return nil, err
	}
	return state, nil
}

func activateAttachment[T any](attachment ReplicatedStateSourceAttachment[T], options ReplicatedStateSourceOptions) (state *AttachedReplicatedState[T], err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			state, err = nil, toError(recovered)
		}
	}()
	snapshot := attachment.Snapshot()
	if err := assertCursor(snapshot.Cursor, "snapshot"); err != nil {
		return nil, err
	}
	reportError := options.OnError
	if reportError == nil {
		reportError = reportErrorAsync
	}
	state = &AttachedReplicatedState[T]{attachment: attachment, cursor: snapshot.Cursor, reportError: reportError}
	state.publisher = newStatePublisher(snapshot.Value, state.report)
	if err := attachment.Activate(state.receive); err != nil {
		return nil, err
	}
	return state, nil
}

// maxSafeInteger is Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

func assertCursor(cursor int, kind string) error {
	if cursor > maxSafeInteger || cursor < -maxSafeInteger {
		return fmt.Errorf("Replicated state source %s cursor must be a safe integer", kind)
	}
	return nil
}
