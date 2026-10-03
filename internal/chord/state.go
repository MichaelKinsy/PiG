package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/chord/delta"
)

// Ports packages/chord/src/services/state.ts.
//
// Delivery model. Each subscription serializes its callbacks, awaiting hydration before updates, independently of the producer and of every other subscriber. A callback that finishes synchronously runs inline on the publishing call stack. A callback that returns a Completion (the Go form of a Promise) suspends the subscription until the completion settles, without blocking the producer. At most maxPendingDeliveries deliveries wait behind the running callback; overflow keeps only the newest pending delivery, so update sequences may skip. Failures are reported to the state's error reporter in isolation and delivery continues. Unsubscribe discards pending work without aborting or joining a callback.

// Delivery kinds carried by ReplicatedStateDelivery.Kind.
const (
	DeliveryHydrate = "hydrate"
	DeliveryUpdate  = "update"
)

const maxPendingDeliveries = 100

// Completion is the Go form of the Promise a listener may return. A listener that has finished returns nil. A listener that is still working returns a channel that receives its failure, or nil, exactly once when it settles; the subscription delivers nothing else until then.
type Completion <-chan error

var uncaughtReporter atomic.Pointer[func(error)]

func init() {
	report := func(err error) { fmt.Fprintf(os.Stderr, "chord: unhandled replicated state error: %v\n", err) }
	uncaughtReporter.Store(&report)
}

// SetUncaughtErrorReporter replaces the receiver of failures that no error reporter handled, the Go form of upstream's reportErrorAsync, which rethrows in a microtask and so reaches the process's uncaught-exception handler. The default writes the failure to standard error and keeps the process running. A replicated state created without an onError option reports through it. It returns the previous receiver and is safe to call while states deliver.
func SetUncaughtErrorReporter(report func(error)) (previous func(error)) {
	return *uncaughtReporter.Swap(&report)
}

// deliveryContext is upstream serviceDeliveryContext(): synthetic deliveries without a caller use the background context.
func deliveryContext() context.Context { return context.Background() }

func reportUncaught(err error) { (*uncaughtReporter.Load())(err) }

// valueListener is one internal subscription callback. A returned error is a synchronous failure; a non-nil Completion is an unfinished asynchronous one.
type valueListener func(value JsonValue, ctx context.Context, delivery ReplicatedStateDelivery) (Completion, error)

type opsListener func(ctx context.Context, ops []Op, sequence int)

type stateDelivery struct {
	value    JsonValue
	ctx      context.Context
	delivery ReplicatedStateDelivery
}

// subscriber is one public subscription, independent of the producer and of other subscribers.
type subscriber struct {
	mu       sync.Mutex
	listener valueListener
	report   func(error)
	pending  []stateDelivery
	running  bool
	started  bool
	closed   bool
}

func newSubscriber(listener valueListener, report func(error)) *subscriber {
	return &subscriber{listener: listener, report: report}
}

func (s *subscriber) push(frame stateDelivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if len(s.pending) == maxPendingDeliveries {
		// A cold replica can queue updates reentrantly before this subscriber's first hydration starts.
		var hydration []stateDelivery
		if !s.started {
			hydration = []stateDelivery{s.pending[0]}
		}
		s.pending = hydration
	}
	s.pending = append(s.pending, frame)
}

func (s *subscriber) drain() {
	s.mu.Lock()
	if s.running || s.closed {
		s.mu.Unlock()
		return
	}
	s.running = true
	for len(s.pending) > 0 {
		frame := s.pending[0]
		s.pending = s.pending[1:]
		s.started = true
		s.mu.Unlock()
		completion, err := callListener(s.listener, frame)
		if err != nil {
			s.reportFailure(err)
		}
		if completion != nil {
			go s.await(completion)
			return
		}
		s.mu.Lock()
	}
	s.running = false
	s.mu.Unlock()
}

func (s *subscriber) await(completion Completion) {
	if err := <-completion; err != nil {
		s.reportFailure(err)
	}
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	s.drain()
}

func (s *subscriber) clear() {
	s.mu.Lock()
	s.pending = nil
	s.mu.Unlock()
}

func (s *subscriber) close() {
	s.mu.Lock()
	s.closed = true
	s.pending = nil
	s.mu.Unlock()
}

func (s *subscriber) reportFailure(err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			reportUncaught(recoveredError(recovered))
		}
	}()
	s.report(err)
}

func callListener(listener valueListener, frame stateDelivery) (completion Completion, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			completion, err = nil, recoveredError(recovered)
		}
	}()
	return listener(frame.value, frame.ctx, frame.delivery)
}

func recoveredError(recovered any) error {
	if asError, ok := recovered.(error); ok {
		return asError
	}
	return fmt.Errorf("%v", recovered)
}

// collected is upstream throwCollectedErrors: one failure as itself, several as an AggregateError.
func collected(errs []error, message string) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return NewAggregateError(message, errs)
	}
}

type publication struct {
	value    JsonValue
	ops      []Op
	sequence int
	ctx      context.Context
}

type stateSubscription struct {
	subscriber *subscriber
	hydrated   int
}

type opsSubscription struct{ listener opsListener }

// stateCore maintains local publication order independently of how revisions are produced (upstream ReplicatedStatePublisher). Values are strict JSON and never mutated in place, so published revisions structurally share unchanged subtrees safely.
type stateCore struct {
	mu          sync.Mutex
	value       JsonValue
	sequence    int
	subscribers []*stateSubscription
	sources     []*opsSubscription
	queue       []publication
	delivering  bool
	reportError func(error)

	// Authoritative states only: the revision tracker, and the lock that serializes Change and Replace.
	changeMu sync.Mutex
	tracker  *delta.Tracker
	mutating atomic.Uint64 // goroutine running a Change mutate callback
}

// stateSource marks values the provider may expose as state members.
type stateSource interface {
	chordState() *stateCore
}

func newStateCore(initial JsonValue, reportError func(error)) *stateCore {
	if reportError == nil {
		reportError = reportUncaught
	}
	return &stateCore{value: initial, reportError: reportError}
}

// snapshot returns the current sequence and value (immutable by contract) atomically.
func (core *stateCore) snapshot() (int, JsonValue) {
	core.mu.Lock()
	defer core.mu.Unlock()
	return core.sequence, core.value
}

// subscribe registers listener and hydrates it synchronously on the caller's stack at the current sequence, including when called from inside another listener.
func (core *stateCore) subscribe(listener valueListener) func() {
	sub := newSubscriber(listener, core.reportError)
	core.mu.Lock()
	entry := &stateSubscription{subscriber: sub, hydrated: core.sequence}
	core.subscribers = append(core.subscribers, entry)
	sub.push(stateDelivery{value: core.value, ctx: deliveryContext(), delivery: ReplicatedStateDelivery{Kind: DeliveryHydrate, Sequence: core.sequence}})
	core.mu.Unlock()
	sub.drain()
	return func() {
		sub.close()
		core.mu.Lock()
		defer core.mu.Unlock()
		for index, candidate := range core.subscribers {
			if candidate == entry {
				core.subscribers = append(core.subscribers[:index:index], core.subscribers[index+1:]...)
				return
			}
		}
	}
}

// subscribeOps registers an exact publication listener: every committed operation batch, in order.
func (core *stateCore) subscribeOps(listener opsListener) func() {
	entry := &opsSubscription{listener: listener}
	core.mu.Lock()
	core.sources = append(core.sources, entry)
	core.mu.Unlock()
	return func() {
		core.mu.Lock()
		defer core.mu.Unlock()
		for index, candidate := range core.sources {
			if candidate == entry {
				core.sources = append(core.sources[:index:index], core.sources[index+1:]...)
				return
			}
		}
	}
}

// enqueue commits a revision and queues its publication. The caller delivers afterwards, so a producer can release its own lock first.
func (core *stateCore) enqueue(value JsonValue, ops []Op, ctx context.Context) {
	core.mu.Lock()
	sequence := core.nextSequenceLocked()
	core.value, core.sequence = value, sequence
	core.queue = append(core.queue, publication{value: value, ops: ops, sequence: sequence, ctx: ctx})
	core.mu.Unlock()
}

func (core *stateCore) nextSequenceLocked() int {
	if len(core.queue) > 0 {
		return core.queue[len(core.queue)-1].sequence + 1
	}
	return core.sequence + 1
}

// deliver publishes every queued revision to exact listeners and subscribers, in order, and returns the exact listeners' failures. Subscriber failures are reported in isolation. A publication made while delivery is running, on this call stack or another goroutine, is delivered by the running loop.
func (core *stateCore) deliver() []error {
	core.mu.Lock()
	if core.delivering {
		core.mu.Unlock()
		return nil
	}
	core.delivering = true
	var errs []error
	for len(core.queue) > 0 {
		next := core.queue[0]
		core.queue = core.queue[1:]
		sources := append([]*opsSubscription(nil), core.sources...)
		subscribers := append([]*stateSubscription(nil), core.subscribers...)
		core.mu.Unlock()
		for _, source := range sources {
			if err := callOps(source.listener, next); err != nil {
				errs = append(errs, err)
			}
		}
		delivery := ReplicatedStateDelivery{Kind: DeliveryUpdate, Sequence: next.sequence}
		for _, entry := range subscribers {
			if next.sequence <= entry.hydrated {
				continue
			}
			entry.subscriber.push(stateDelivery{value: next.value, ctx: next.ctx, delivery: delivery})
			entry.subscriber.drain()
		}
		core.mu.Lock()
	}
	core.delivering = false
	core.mu.Unlock()
	return errs
}

// publish is enqueue followed by deliver, for producers that hold no lock of their own.
func (core *stateCore) publish(value JsonValue, ops []Op, ctx context.Context) []error {
	core.enqueue(value, ops, ctx)
	return core.deliver()
}

func callOps(listener opsListener, next publication) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = recoveredError(recovered)
		}
	}()
	listener(next.ctx, next.ops, next.sequence)
	return nil
}

func typedListener[T any](listener func(T, context.Context, ReplicatedStateDelivery)) valueListener {
	return func(value JsonValue, ctx context.Context, delivery ReplicatedStateDelivery) (Completion, error) {
		decoded, err := decodeValue[T](value)
		if err != nil {
			return nil, err
		}
		listener(decoded, ctx, delivery)
		return nil, nil
	}
}

func typedAsyncListener[T any](listener func(T, context.Context, ReplicatedStateDelivery) Completion) valueListener {
	return func(value JsonValue, ctx context.Context, delivery ReplicatedStateDelivery) (Completion, error) {
		decoded, err := decodeValue[T](value)
		if err != nil {
			return nil, err
		}
		return listener(decoded, ctx, delivery), nil
	}
}

func decodeValue[T any](value JsonValue) (T, error) {
	var decoded T
	encoded, err := json.Marshal(value)
	if err != nil {
		return decoded, err
	}
	err = json.Unmarshal(encoded, &decoded)
	return decoded, err
}

func toStateValue(value any) (JsonValue, error) {
	stored, err := chordjson.Stored(value)
	if err != nil {
		return nil, fmt.Errorf("replicated state value is not strict JSON: %w", err)
	}
	switch stored.(type) {
	case map[string]any, []any:
		return stored, nil
	}
	return nil, errors.New("replicated state value must be a JSON object or array")
}

// MutableReplicatedState is upstream replicatedState(initial): initialized, publishable state suitable for exposing as a service member. It implements MutableReplicatedStateOf[T] and is safe for concurrent use.
//
// Change and Replace serialize with each other. Calling Change or Replace on the same state from inside a mutate callback is rejected, as upstream. Exact publication listeners' failures are returned by the Change or Replace call that delivered them, after the value has been committed. Subscriber callback failures are reported to the uncaught error reporter (SetUncaughtErrorReporter).
type MutableReplicatedState[T any] struct {
	core *stateCore
}

var _ MutableReplicatedStateOf[map[string]any] = (*MutableReplicatedState[map[string]any])(nil)

// NewReplicatedState creates state whose strict JSON representation is an object or array, as upstream's `T extends object`. It takes ownership of the converted value.
func NewReplicatedState[T any](initial T) (*MutableReplicatedState[T], error) {
	stored, err := toStateValue(initial)
	if err != nil {
		return nil, err
	}
	core := newStateCore(stored, nil)
	core.tracker = delta.Track(stored)
	return &MutableReplicatedState[T]{core: core}, nil
}

func (state *MutableReplicatedState[T]) chordState() *stateCore { return state.core }

// Sequence is the number of committed publications.
func (state *MutableReplicatedState[T]) Sequence() int {
	sequence, _ := state.core.snapshot()
	return sequence
}

// Value returns a detached copy of the current value.
func (state *MutableReplicatedState[T]) Value() T {
	_, value := state.core.snapshot()
	decoded, err := decodeValue[T](value)
	if err != nil {
		panic(fmt.Sprintf("chord: stored state no longer decodes: %v", err))
	}
	return decoded
}

// Change applies mutate to a detached draft and atomically publishes the result. A mutate error or panic discards the draft and returns the failure. An unchanged draft publishes nothing.
func (state *MutableReplicatedState[T]) Change(ctx context.Context, mutate func(T) error) error {
	core := state.core
	self := currentGoroutine()
	if core.mutating.Load() == self {
		return errors.New("Replicated state cannot be changed reentrantly from a change callback")
	}
	core.changeMu.Lock()
	draft, err := decodeValue[T](core.tracker.Value())
	if err != nil {
		core.changeMu.Unlock()
		return err
	}
	core.mutating.Store(self)
	err = callMutate(mutate, draft)
	core.mutating.Store(0)
	if err != nil {
		core.changeMu.Unlock()
		return err
	}
	candidate, err := toStateValue(draft)
	if err != nil {
		core.changeMu.Unlock()
		return err
	}
	prepared, err := core.tracker.PrepareCandidate(candidate)
	if err != nil {
		core.changeMu.Unlock()
		return err
	}
	return state.adoptAndDeliver(ctx, prepared)
}

func callMutate[T any](mutate func(T) error, draft T) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = recoveredError(recovered)
		}
	}()
	return mutate(draft)
}

// Replace atomically publishes a complete replacement value, taking ownership of its converted form.
func (state *MutableReplicatedState[T]) Replace(ctx context.Context, value T) error {
	core := state.core
	if core.mutating.Load() == currentGoroutine() {
		return errors.New("Replicated state cannot be replaced from a change callback")
	}
	next, err := toStateValue(value)
	if err != nil {
		return err
	}
	core.changeMu.Lock()
	return state.adoptAndDeliver(ctx, core.tracker.PrepareReplace(next))
}

// adoptAndDeliver is entered with changeMu held and returns with it released.
func (state *MutableReplicatedState[T]) adoptAndDeliver(ctx context.Context, prepared *delta.Prepared) error {
	core := state.core
	if err := core.tracker.Adopt(prepared); err != nil {
		core.changeMu.Unlock()
		return err
	}
	if len(prepared.Ops) == 0 {
		core.changeMu.Unlock()
		return nil
	}
	core.enqueue(prepared.Value, prepared.Ops, ctx)
	core.changeMu.Unlock()
	return collected(core.deliver(), "Replicated state listeners failed")
}

// lastSequence is the sequence of the newest committed publication, delivered or not.
func (core *stateCore) lastSequence() int {
	core.mu.Lock()
	defer core.mu.Unlock()
	return core.nextSequenceLocked() - 1
}

// Subscribe delivers a hydrate at the current sequence, then each later update in sequence order, as ReplicatedState.subscribe does upstream. The listener finishes synchronously; its failures are reported, not returned.
func (state *MutableReplicatedState[T]) Subscribe(listener func(T, context.Context, ReplicatedStateDelivery)) (func(), error) {
	return state.core.subscribe(typedListener(listener)), nil
}

// SubscribeAsync is Subscribe for a listener that may still be working when it returns: the subscription waits for the returned Completion before its next delivery.
func (state *MutableReplicatedState[T]) SubscribeAsync(listener func(T, context.Context, ReplicatedStateDelivery) Completion) (func(), error) {
	return state.core.subscribe(typedAsyncListener(listener)), nil
}

// ReplicatedStateSourceSnapshot is the fixed immutable snapshot an attachment captured atomically.
type ReplicatedStateSourceSnapshot struct {
	Value  JsonValue
	Cursor int
}

// ReplicatedStateSourceFrame is one immutable authoritative revision committed after an attachment snapshot.
type ReplicatedStateSourceFrame struct {
	// Cursor is the monotonic source cursor. The first frame after a snapshot is Snapshot.Cursor+1.
	Cursor int
	// Value is the exact immutable value produced by this commit.
	Value JsonValue
	// Ops is the exact immutable operation batch that produced Value from the preceding source revision.
	Ops     []Op
	Context context.Context
}

// ReplicatedStateSourceAttachment is one live attachment to a source.
type ReplicatedStateSourceAttachment interface {
	// Snapshot is the fixed snapshot captured at the atomic attachment boundary.
	Snapshot() ReplicatedStateSourceSnapshot
	// Activate installs the sole listener and synchronously drains every buffered frame in source commit order. It is single-use. After it begins, every new committed frame is also delivered in order until disposal, including commits made reentrantly while a prior frame is being delivered.
	Activate(listener func(ReplicatedStateSourceFrame))
	// Dispose stops delivery and releases source resources. It must be idempotent.
	Dispose()
}

// ReplicatedStateSource is an authoritative immutable revision source. Attach must synchronously and atomically capture one snapshot and register the returned attachment to buffer every later committed frame: the snapshot includes every commit before that boundary, buffered frames every commit after it, with no overlap or gap. Snapshot values, frame values and operation batches are immutable and stay valid after delivery. Chord only publishes these references; it never applies or re-diffs them.
type ReplicatedStateSource interface {
	Attach() ReplicatedStateSourceAttachment
}

// ReplicatedStateSourceOptions configures an attached state.
type ReplicatedStateSourceOptions struct {
	// OnError receives source-contract and publication-listener failures without throwing them into the source. The default is the uncaught error reporter.
	OnError func(error)
}

// AttachedReplicatedState is a synchronously hydrated publication-only state backed by one source attachment (upstream AttachedReplicatedState).
type AttachedReplicatedState[T any] struct {
	core        *stateCore
	attachment  ReplicatedStateSourceAttachment
	reportError func(error)
	mu          sync.Mutex
	cursor      int
	disposed    bool
}

var _ ReplicatedStateOf[map[string]any] = (*AttachedReplicatedState[map[string]any])(nil)

const maxSafeInteger = 1<<53 - 1

func assertCursor(cursor int, kind string) error {
	if cursor > maxSafeInteger || cursor < -maxSafeInteger {
		return fmt.Errorf("Replicated state source %s cursor must be a safe integer", kind)
	}
	return nil
}

// AttachReplicatedState is upstream replicatedState(source, options): it attaches one state to the source and activates it before returning. A failed attachment is disposed.
func AttachReplicatedState[T any](source ReplicatedStateSource, options ReplicatedStateSourceOptions) (*AttachedReplicatedState[T], error) {
	attachment := source.Attach()
	state, err := newAttached[T](attachment, options)
	if err == nil {
		state.activate()
		return state, nil
	}
	if disposeErr := disposeAttachment(attachment); disposeErr != nil {
		return nil, NewAggregateError("Failed to attach replicated state source", []error{err, disposeErr})
	}
	return nil, err
}

func disposeAttachment(attachment ReplicatedStateSourceAttachment) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = recoveredError(recovered)
		}
	}()
	attachment.Dispose()
	return nil
}

func newAttached[T any](attachment ReplicatedStateSourceAttachment, options ReplicatedStateSourceOptions) (*AttachedReplicatedState[T], error) {
	snapshot := attachment.Snapshot()
	if err := assertCursor(snapshot.Cursor, "snapshot"); err != nil {
		return nil, err
	}
	state := &AttachedReplicatedState[T]{attachment: attachment, cursor: snapshot.Cursor, reportError: options.OnError}
	if state.reportError == nil {
		state.reportError = reportUncaught
	}
	state.core = newStateCore(snapshot.Value, state.report)
	return state, nil
}

func (state *AttachedReplicatedState[T]) chordState() *stateCore { return state.core }

// Value is the last published value, which stays readable after Dispose.
func (state *AttachedReplicatedState[T]) Value() T {
	_, value := state.core.snapshot()
	decoded, err := decodeValue[T](value)
	if err != nil {
		panic(fmt.Sprintf("chord: attached state no longer decodes: %v", err))
	}
	return decoded
}

// Subscribe is MutableReplicatedState.Subscribe for an attached state.
func (state *AttachedReplicatedState[T]) Subscribe(listener func(T, context.Context, ReplicatedStateDelivery)) (func(), error) {
	return state.core.subscribe(typedListener(listener)), nil
}

// SubscribeAsync is MutableReplicatedState.SubscribeAsync for an attached state.
func (state *AttachedReplicatedState[T]) SubscribeAsync(listener func(T, context.Context, ReplicatedStateDelivery) Completion) (func(), error) {
	return state.core.subscribe(typedAsyncListener(listener)), nil
}

func (state *AttachedReplicatedState[T]) activate() {
	state.attachment.Activate(state.receive)
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

func (state *AttachedReplicatedState[T]) receive(frame ReplicatedStateSourceFrame) {
	state.mu.Lock()
	if state.disposed {
		state.mu.Unlock()
		return
	}
	failure := state.advanceLocked(frame)
	state.mu.Unlock()
	if failure != nil {
		state.fail(failure)
		return
	}
	ctx := frame.Context
	if ctx == nil {
		ctx = deliveryContext()
	}
	switch errs := state.core.publish(frame.Value, frame.Ops, ctx); len(errs) {
	case 0:
	case 1:
		state.report(errs[0])
	default:
		state.report(NewAggregateError("Replicated state listeners failed", errs))
	}
}

func (state *AttachedReplicatedState[T]) advanceLocked(frame ReplicatedStateSourceFrame) error {
	if err := assertCursor(frame.Cursor, "frame"); err != nil {
		return err
	}
	if expected := state.cursor + 1; frame.Cursor != expected {
		return fmt.Errorf("Replicated state source cursor has a gap: expected %d, received %d", expected, frame.Cursor)
	}
	state.cursor = frame.Cursor
	return nil
}

func (state *AttachedReplicatedState[T]) fail(failure error) {
	state.mu.Lock()
	if state.disposed {
		state.mu.Unlock()
		return
	}
	state.disposed = true
	state.mu.Unlock()
	if err := disposeAttachment(state.attachment); err != nil {
		state.report(NewAggregateError("Replicated state source contract failed", []error{failure, err}))
		return
	}
	state.report(failure)
}

func (state *AttachedReplicatedState[T]) report(err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			reportUncaught(recoveredError(recovered))
		}
	}()
	state.reportError(err)
}

// FacetState imports the state of an isolated provider into the existing Chord state publication path. It preserves the source's sequence and operation batches rather than recomputing a different diff.
type FacetState struct {
	core *stateCore
}

// NewFacetState installs an initialized source snapshot. The loaded generation owns subsequent Apply calls and source-listener cleanup.
func NewFacetState(value JsonValue, sequence int) (*FacetState, error) {
	if sequence < 0 {
		return nil, errors.New("Replicated state sequence must not be negative")
	}
	stored, err := toStateValue(value)
	if err != nil {
		return nil, err
	}
	core := newStateCore(stored, nil)
	core.sequence = sequence
	return &FacetState{core: core}, nil
}

// Apply publishes one gap-free source operation batch before returning to its producer. Exact listener failures surface after the value commits, as for native mutable state.
func (state *FacetState) Apply(ctx context.Context, sequence int, ops []Op) error {
	core := state.core
	core.changeMu.Lock()
	if sequence != core.lastSequence()+1 {
		core.changeMu.Unlock()
		return errors.New("Replicated state update sequence has a gap")
	}
	_, value := core.snapshot()
	next, err := delta.ApplyImmutable(value, ops)
	if err == nil {
		err = validateRevision(ops)
	}
	if err != nil {
		core.changeMu.Unlock()
		return err
	}
	core.enqueue(next, ops, ctx)
	core.changeMu.Unlock()
	return collected(core.deliver(), "Replicated state listeners failed")
}

// replicaCore is the cold read-only consumer state that installs a base snapshot and applies gap-free operation batches (upstream ReplicatedStateReplica).
type replicaCore struct {
	mu          sync.Mutex
	reportError func(error)
	subscribers []*subscriber
	value       JsonValue
	sequence    int
	hydrated    bool
}

func newReplica(reportError func(error)) *replicaCore {
	if reportError == nil {
		reportError = reportUncaught
	}
	return &replicaCore{reportError: reportError}
}

// snapshotValue returns the current JSON value and whether the replica is hydrated. The value is immutable by contract.
func (replica *replicaCore) snapshotValue() (JsonValue, bool) {
	replica.mu.Lock()
	defer replica.mu.Unlock()
	return replica.value, replica.hydrated
}

// subscribe registers listener; a hydrated replica delivers its retained value immediately as kind "hydrate".
func (replica *replicaCore) subscribe(listener valueListener) func() {
	sub := newSubscriber(listener, replica.reportError)
	replica.mu.Lock()
	replica.subscribers = append(replica.subscribers, sub)
	if replica.hydrated {
		sub.push(stateDelivery{value: replica.value, ctx: deliveryContext(), delivery: ReplicatedStateDelivery{Kind: DeliveryHydrate, Sequence: replica.sequence}})
	}
	replica.mu.Unlock()
	sub.drain()
	return func() {
		sub.close()
		replica.mu.Lock()
		defer replica.mu.Unlock()
		for index, candidate := range replica.subscribers {
			if candidate == sub {
				replica.subscribers = append(replica.subscribers[:index:index], replica.subscribers[index+1:]...)
				return
			}
		}
	}
}

// validateRevision checks the payloads an operation batch introduces. Containers a replica already holds were validated when they arrived, so only inserted values can make a revision invalid.
func validateRevision(ops []Op) error {
	for _, op := range ops {
		var payload JsonValue
		switch op.Verb() {
		case "r", "s":
			payload = op[len(op)-1]
		case "p":
			payload = op[4]
		default:
			continue
		}
		if err := chordjson.Validate(payload); err != nil {
			return err
		}
	}
	return nil
}

// hydrate installs a base batch at sequence. valid is evaluated under the replica lock; a false result means the binding fenced this delivery (rebind or dispose) and it is discarded.
func (replica *replicaCore) hydrate(ctx context.Context, sequence int, ops []Op, valid func() bool) error {
	replica.mu.Lock()
	if valid != nil && !valid() {
		replica.mu.Unlock()
		return nil
	}
	next, err := replica.applyLocked(nil, ops, true)
	if err != nil {
		replica.clearLocked()
		replica.mu.Unlock()
		return err
	}
	replica.value, replica.sequence, replica.hydrated = next, sequence, true
	replica.deliverLocked(next, ctx, ReplicatedStateDelivery{Kind: DeliveryHydrate, Sequence: sequence})
	return nil
}

// update applies the next gap-free batch.
func (replica *replicaCore) update(ctx context.Context, sequence int, ops []Op, valid func() bool) error {
	replica.mu.Lock()
	if valid != nil && !valid() {
		replica.mu.Unlock()
		return nil
	}
	if !replica.hydrated {
		replica.mu.Unlock()
		return errors.New("Replicated state received an update before hydration")
	}
	if sequence != replica.sequence+1 {
		replica.clearLocked()
		replica.mu.Unlock()
		return errors.New("Replicated state update sequence has a gap")
	}
	next, err := replica.applyLocked(replica.value, ops, false)
	if err != nil {
		replica.clearLocked()
		replica.mu.Unlock()
		return err
	}
	replica.value, replica.sequence = next, sequence
	replica.deliverLocked(next, ctx, ReplicatedStateDelivery{Kind: DeliveryUpdate, Sequence: sequence})
	return nil
}

func (replica *replicaCore) applyLocked(target JsonValue, ops []Op, base bool) (JsonValue, error) {
	if base && !delta.IsBase(ops) {
		return nil, errors.New("Replicated state snapshot is not a base operation batch")
	}
	next, err := delta.ApplyImmutable(target, ops)
	if err != nil {
		return nil, err
	}
	if err := validateRevision(ops); err != nil {
		return nil, err
	}
	return next, nil
}

// deliverLocked is entered with mu held and returns with it released. It enqueues the frame for every subscriber before user code can publish another revision reentrantly.
func (replica *replicaCore) deliverLocked(value JsonValue, ctx context.Context, delivery ReplicatedStateDelivery) {
	subscribers := append([]*subscriber(nil), replica.subscribers...)
	frame := stateDelivery{value: value, ctx: ctx, delivery: delivery}
	for _, sub := range subscribers {
		sub.push(frame)
	}
	replica.mu.Unlock()
	for _, sub := range subscribers {
		sub.drain()
	}
}

func (replica *replicaCore) clear() {
	replica.mu.Lock()
	defer replica.mu.Unlock()
	replica.clearLocked()
}

func (replica *replicaCore) clearLocked() {
	replica.value, replica.sequence, replica.hydrated = nil, 0, false
	for _, sub := range replica.subscribers {
		sub.clear()
	}
}

// ReplicatedStateReplica is a consumer's guarded handle to a replicated state member of a remote service. Like upstream's member view, each Value, Load and Subscribe checks the holder's access (binding disposal, facet revocation, closed keyed observation). It is untyped; use TypedReplica for a typed view.
type ReplicatedStateReplica struct {
	core   *replicaCore
	access func() error
}

// Load returns the current JSON value and whether the replica is hydrated, or the access error. The value is immutable by contract.
func (replica *ReplicatedStateReplica) Load() (JsonValue, bool, error) {
	if replica.access != nil {
		if err := replica.access(); err != nil {
			return nil, false, err
		}
	}
	value, hydrated := replica.core.snapshotValue()
	return value, hydrated, nil
}

// Value is Load for callers that treat revocation as a contract violation: an access failure panics with its error (upstream's value property throws) rather than reading as unhydrated. Use Load to handle it as an error.
func (replica *ReplicatedStateReplica) Value() (JsonValue, bool) {
	value, hydrated, err := replica.Load()
	if err != nil {
		panic(err)
	}
	return value, hydrated
}

// Subscribe registers listener after an access check; a hydrated replica delivers its retained value immediately as kind "hydrate". A listener failure is reported to the binding's error reporter and delivery continues.
func (replica *ReplicatedStateReplica) Subscribe(listener func(JsonValue, context.Context, ReplicatedStateDelivery) error) (func(), error) {
	return replica.subscribe(func(value JsonValue, ctx context.Context, delivery ReplicatedStateDelivery) (Completion, error) {
		return nil, listener(value, ctx, delivery)
	})
}

// SubscribeAsync is Subscribe for a listener that may still be working when it returns.
func (replica *ReplicatedStateReplica) SubscribeAsync(listener func(JsonValue, context.Context, ReplicatedStateDelivery) Completion) (func(), error) {
	return replica.subscribe(func(value JsonValue, ctx context.Context, delivery ReplicatedStateDelivery) (Completion, error) {
		return listener(value, ctx, delivery), nil
	})
}

func (replica *ReplicatedStateReplica) subscribe(listener valueListener) (func(), error) {
	if replica.access != nil {
		if err := replica.access(); err != nil {
			return nil, err
		}
	}
	return replica.core.subscribe(listener), nil
}

// ReplicaOf is a typed read view of a replica. It implements ReplicatedStateOf[T]; Value returns the zero T before hydration (use Load to distinguish).
type ReplicaOf[T any] struct {
	replica *ReplicatedStateReplica
}

var _ ReplicatedStateOf[map[string]any] = ReplicaOf[map[string]any]{}

// TypedReplica returns a typed view of replica.
func TypedReplica[T any](replica *ReplicatedStateReplica) ReplicaOf[T] {
	return ReplicaOf[T]{replica: replica}
}

// Load returns a detached decoded value and whether the replica is hydrated.
func (view ReplicaOf[T]) Load() (T, bool, error) {
	value, hydrated, err := view.replica.Load()
	if err != nil {
		var zero T
		return zero, false, err
	}
	if !hydrated {
		var zero T
		return zero, false, nil
	}
	decoded, err := decodeValue[T](value)
	return decoded, true, err
}

// Value returns a detached decoded value, or the zero T before hydration (upstream undefined). An access or decoding failure panics with its error instead of reading as an empty value; use Load to handle it.
func (view ReplicaOf[T]) Value() T {
	decoded, _, err := view.Load()
	if err != nil {
		panic(err)
	}
	return decoded
}

// Subscribe decodes each delivery into a detached T; it fails only the access check.
func (view ReplicaOf[T]) Subscribe(listener func(T, context.Context, ReplicatedStateDelivery)) (func(), error) {
	return view.replica.subscribe(typedListener(listener))
}

// SubscribeAsync is Subscribe for a listener that may still be working when it returns.
func (view ReplicaOf[T]) SubscribeAsync(listener func(T, context.Context, ReplicatedStateDelivery) Completion) (func(), error) {
	return view.replica.subscribe(typedAsyncListener(listener))
}
