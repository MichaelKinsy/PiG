package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// Delivery kinds carried by pico3.ReplicatedStateDelivery.Kind.
const (
	DeliveryHydrate = "hydrate"
	DeliveryUpdate  = "update"
)

// deliveryContext is upstream serviceDeliveryContext(): synthetic deliveries
// without a caller use the background context.
func deliveryContext() context.Context { return context.Background() }

type valueListener func(value pico3.JsonValue, ctx context.Context, delivery pico3.ReplicatedStateDelivery) error

type opsListener func(ctx context.Context, ops []pico3.Op, sequence int)

type stateListener struct {
	listener valueListener
	hydrated int
	active   bool
	// hydrating is set while Subscribe delivers the initial value on its own
	// call stack; a drain on another goroutine parks later updates in pending
	// so the listener never observes an update before its hydration.
	hydrating bool
	hydrator  uint64 // goroutine running the hydration
	pending   []stateJob
}

type stateSourceListener struct{ listener opsListener }

type stateJob struct {
	// publication
	value    pico3.JsonValue
	ops      []pico3.Op
	sequence int
	ctx      context.Context
}

// stateCore is the untyped upstream MutableReplicatedStateImpl. It stores the
// strict JSON representation; stored values are never mutated in place, so
// published revisions structurally share unchanged subtrees safely.
type stateCore struct {
	changeMu   sync.Mutex // serializes Change/Replace read-modify-write
	mu         sync.Mutex
	value      pico3.JsonValue
	sequence   int
	tracker    *pico3.Tracker
	listeners  []*stateListener
	sources    []*stateSourceListener
	queue      []stateJob
	delivering bool
	mutating   atomic.Uint64 // goroutine running a Change mutate callback
}

// stateSource marks values the provider may expose as state members.
type stateSource interface {
	chordState() *stateCore
}

func newStateCore(initial pico3.JsonValue) *stateCore {
	core := &stateCore{value: initial}
	if object, ok := initial.(map[string]any); ok {
		core.tracker = pico3.Track(cloneObject(object))
		core.tracker.Flush()
	}
	return core
}

func cloneObject(object map[string]any) map[string]any {
	stored, _ := pico3.ToStored(object)
	cloned, _ := stored.(map[string]any)
	return cloned
}

// snapshot returns the current sequence and value (immutable by contract).
func (core *stateCore) snapshot() (int, pico3.JsonValue) {
	core.mu.Lock()
	defer core.mu.Unlock()
	return core.sequence, core.value
}

// diffLocked returns the ops that transform the current value into next.
func (core *stateCore) diffLocked(next pico3.JsonValue) []pico3.Op {
	nextObject, nextIsObject := next.(map[string]any)
	if core.tracker != nil && nextIsObject {
		state := core.tracker.State()
		for key := range state {
			delete(state, key)
		}
		maps.Copy(state, nextObject)
		return core.tracker.Flush()
	}
	if reflect.DeepEqual(core.value, next) {
		return nil
	}
	if nextIsObject {
		core.tracker = pico3.Track(cloneObject(nextObject))
		core.tracker.Flush()
	} else {
		core.tracker = nil
	}
	return []pico3.Op{{"r", next}}
}

// drainLocked is entered with mu held and returns with it released.
func (core *stateCore) drainLocked() error {
	if core.delivering {
		core.mu.Unlock()
		return nil
	}
	core.delivering = true
	var errs []error
	for len(core.queue) > 0 {
		job := core.queue[0]
		core.queue = core.queue[1:]
		sources := append([]*stateSourceListener(nil), core.sources...)
		listeners := append([]*stateListener(nil), core.listeners...)
		core.mu.Unlock()
		for _, source := range sources {
			if err := callOps(source.listener, job); err != nil {
				errs = append(errs, err)
			}
		}
		delivery := pico3.ReplicatedStateDelivery{Kind: DeliveryUpdate, Sequence: job.sequence}
		for _, entry := range listeners {
			if !core.acceptUpdate(entry, job) {
				continue
			}
			if err := callValue(entry.listener, job.value, job.ctx, delivery); err != nil {
				errs = append(errs, err)
			}
		}
		core.mu.Lock()
	}
	core.delivering = false
	core.mu.Unlock()
	if len(errs) > 1 {
		return fmt.Errorf("replicated state listeners failed: %w", errors.Join(errs...))
	}
	return joinErrors(errs)
}

// acceptUpdate reports whether job should be delivered to entry now. An
// update for a listener still hydrating on another goroutine is parked for
// that Subscribe call to deliver after the hydration.
func (core *stateCore) acceptUpdate(entry *stateListener, job stateJob) bool {
	core.mu.Lock()
	defer core.mu.Unlock()
	if !entry.active || job.sequence <= entry.hydrated {
		return false
	}
	if entry.hydrating {
		// A publication made on the hydrating call stack itself (a Change
		// inside the hydration listener) is delivered inline, as upstream,
		// unless earlier parked updates must go first.
		if entry.hydrator == currentGoroutine() && len(entry.pending) == 0 {
			return true
		}
		entry.pending = append(entry.pending, job)
		return false
	}
	return true
}

func callOps(listener opsListener, job stateJob) (err error) {
	defer recoverInto(&err)
	listener(job.ctx, job.ops, job.sequence)
	return nil
}

func callValue(listener valueListener, value pico3.JsonValue, ctx context.Context, delivery pico3.ReplicatedStateDelivery) (err error) {
	defer recoverInto(&err)
	return listener(value, ctx, delivery)
}

func recoverInto(err *error) {
	if recovered := recover(); recovered != nil {
		if asError, ok := recovered.(error); ok {
			*err = asError
		} else {
			*err = fmt.Errorf("%v", recovered)
		}
	}
}

// subscribe registers listener and hydrates it synchronously on the caller's
// stack at the current sequence (upstream MutableReplicatedStateImpl.subscribe),
// including when called from inside another listener. A hydration failure
// unregisters the listener and is returned by this call.
func (core *stateCore) subscribe(listener valueListener) (func(), error) {
	entry := &stateListener{listener: listener, active: true, hydrating: true, hydrator: currentGoroutine()}
	core.mu.Lock()
	sequence, value := core.sequence, core.value
	entry.hydrated = sequence
	core.listeners = append(core.listeners, entry)
	core.mu.Unlock()
	if err := callValue(listener, value, deliveryContext(), pico3.ReplicatedStateDelivery{Kind: DeliveryHydrate, Sequence: sequence}); err != nil {
		core.removeListener(entry)
		return nil, err
	}
	var errs []error
	for {
		core.mu.Lock()
		if len(entry.pending) == 0 || !entry.active {
			entry.pending = nil
			entry.hydrating = false
			core.mu.Unlock()
			break
		}
		job := entry.pending[0]
		entry.pending = entry.pending[1:]
		core.mu.Unlock()
		if err := callValue(listener, job.value, job.ctx, pico3.ReplicatedStateDelivery{Kind: DeliveryUpdate, Sequence: job.sequence}); err != nil {
			errs = append(errs, err)
		}
	}
	return func() { core.removeListener(entry) }, joinErrors(errs)
}

func (core *stateCore) removeListener(entry *stateListener) {
	core.mu.Lock()
	defer core.mu.Unlock()
	entry.active = false
	for index, candidate := range core.listeners {
		if candidate == entry {
			core.listeners = append(core.listeners[:index:index], core.listeners[index+1:]...)
			return
		}
	}
}

// subscribeOps registers a provider operation-stream listener.
func (core *stateCore) subscribeOps(listener opsListener) func() {
	entry := &stateSourceListener{listener: listener}
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

// MutableReplicatedState is upstream replicatedState(initial): initialized,
// publishable state suitable for exposing as a service member. It implements
// pico3.MutableReplicatedStateOf[T] and is safe for concurrent use.
//
// Change and Replace serialize with each other. Calling Change or Replace on
// the same state from inside a mutate callback is rejected, as upstream.
// Listener failures are returned by the
// Change/Replace/Subscribe call that drained the delivery, after the value has
// been committed, as upstream.
type MutableReplicatedState[T any] struct {
	core *stateCore
}

var _ pico3.MutableReplicatedStateOf[map[string]any] = (*MutableReplicatedState[map[string]any])(nil)

// NewReplicatedState creates state whose strict JSON representation is an
// object or array, as upstream's `T extends object`.
func NewReplicatedState[T any](initial T) (*MutableReplicatedState[T], error) {
	stored, err := toStateValue(initial)
	if err != nil {
		return nil, err
	}
	return &MutableReplicatedState[T]{core: newStateCore(stored)}, nil
}

func toStateValue(value any) (pico3.JsonValue, error) {
	stored, err := pico3.ToStored(value)
	if err != nil {
		return nil, fmt.Errorf("replicated state value is not strict JSON: %w", err)
	}
	switch stored.(type) {
	case map[string]any, []any:
		return stored, nil
	}
	return nil, errors.New("replicated state value must be a JSON object or array")
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

// Change applies mutate to a detached draft and atomically publishes the
// result. A mutate error or panic discards the draft and returns the failure.
// An unchanged draft publishes nothing.
func (state *MutableReplicatedState[T]) Change(ctx context.Context, mutate func(T) error) error {
	self := currentGoroutine()
	if state.core.mutating.Load() == self {
		return errors.New("Replicated state cannot be changed reentrantly from a change callback")
	}
	state.core.changeMu.Lock()
	_, current := state.core.snapshot()
	draft, err := decodeValue[T](current)
	if err != nil {
		state.core.changeMu.Unlock()
		return err
	}
	state.core.mutating.Store(self)
	err = callMutate(mutate, draft)
	state.core.mutating.Store(0)
	if err != nil {
		state.core.changeMu.Unlock()
		return err
	}
	next, err := toStateValue(draft)
	if err != nil {
		state.core.changeMu.Unlock()
		return err
	}
	return state.commitUnlocking(ctx, next)
}

func callMutate[T any](mutate func(T) error, draft T) (err error) {
	defer recoverInto(&err)
	return mutate(draft)
}

// Replace atomically publishes a complete detached value.
func (state *MutableReplicatedState[T]) Replace(ctx context.Context, value T) error {
	if state.core.mutating.Load() == currentGoroutine() {
		return errors.New("Replicated state cannot be replaced from a change callback")
	}
	next, err := toStateValue(value)
	if err != nil {
		return err
	}
	state.core.changeMu.Lock()
	return state.commitUnlocking(ctx, next)
}

func (state *MutableReplicatedState[T]) commitUnlocking(ctx context.Context, next pico3.JsonValue) error {
	state.core.mu.Lock()
	ops := state.core.diffLocked(next)
	if len(ops) == 0 {
		state.core.mu.Unlock()
		state.core.changeMu.Unlock()
		return nil
	}
	state.core.value = next
	state.core.sequence++
	state.core.queue = append(state.core.queue, stateJob{value: next, ops: ops, sequence: state.core.sequence, ctx: ctx})
	state.core.changeMu.Unlock()
	return state.core.drainLocked()
}

// Subscribe delivers a hydrate at the current sequence, then each later
// update in sequence order. A failing hydration unregisters the listener and
// returns its error.
func (state *MutableReplicatedState[T]) Subscribe(listener func(T, context.Context, pico3.ReplicatedStateDelivery)) (func(), error) {
	return state.core.subscribe(typedListener(listener))
}

func typedListener[T any](listener func(T, context.Context, pico3.ReplicatedStateDelivery)) valueListener {
	return func(value pico3.JsonValue, ctx context.Context, delivery pico3.ReplicatedStateDelivery) error {
		decoded, err := decodeValue[T](value)
		if err != nil {
			return err
		}
		listener(decoded, ctx, delivery)
		return nil
	}
}

func decodeValue[T any](value pico3.JsonValue) (T, error) {
	var decoded T
	encoded, err := json.Marshal(value)
	if err != nil {
		return decoded, err
	}
	err = json.Unmarshal(encoded, &decoded)
	return decoded, err
}

// replicaCore is the cold read-only consumer state that installs a base
// snapshot and applies gap-free operation batches (upstream
// ReplicatedStateReplica).
type replicaCore struct {
	mu          sync.Mutex
	reportError func(error)
	listeners   []*replicaListener
	value       pico3.JsonValue
	sequence    int
	hydrated    bool
}

type replicaListener struct {
	listener valueListener
}

func newReplica(reportError func(error)) *replicaCore {
	if reportError == nil {
		reportError = func(error) {}
	}
	return &replicaCore{reportError: reportError}
}

// Value returns the current JSON value and whether the replica is hydrated.
// The value is immutable by contract; later updates never mutate it.
func (replica *replicaCore) snapshotValue() (pico3.JsonValue, bool) {
	replica.mu.Lock()
	defer replica.mu.Unlock()
	return replica.value, replica.hydrated
}

// subscribe registers listener; a hydrated replica delivers its retained value
// immediately as kind "hydrate". Listener errors go to the binding's onError.
func (replica *replicaCore) subscribe(listener func(pico3.JsonValue, context.Context, pico3.ReplicatedStateDelivery) error) func() {
	entry := &replicaListener{listener: listener}
	replica.mu.Lock()
	replica.listeners = append(replica.listeners, entry)
	value, sequence, hydrated := replica.value, replica.sequence, replica.hydrated
	replica.mu.Unlock()
	if hydrated {
		replica.deliver(entry, value, deliveryContext(), pico3.ReplicatedStateDelivery{Kind: DeliveryHydrate, Sequence: sequence})
	}
	return func() {
		replica.mu.Lock()
		defer replica.mu.Unlock()
		for index, candidate := range replica.listeners {
			if candidate == entry {
				replica.listeners = append(replica.listeners[:index:index], replica.listeners[index+1:]...)
				return
			}
		}
	}
}

// hydrate installs a base batch at sequence.
// valid is evaluated under the replica lock; a false result means the
// binding fenced this delivery (rebind/dispose) and it is discarded.
func (replica *replicaCore) hydrate(ctx context.Context, sequence int, ops []pico3.Op, valid func() bool) error {
	replica.mu.Lock()
	if valid != nil && !valid() {
		replica.mu.Unlock()
		return nil
	}
	if !pico3.IsBase(ops) {
		replica.clearLocked()
		replica.mu.Unlock()
		return errors.New("Replicated state snapshot is not a base operation batch")
	}
	next, err := pico3.ApplyImmutable(nil, ops)
	if err != nil {
		replica.clearLocked()
		replica.mu.Unlock()
		return err
	}
	replica.value, replica.sequence, replica.hydrated = next, sequence, true
	listeners := append([]*replicaListener(nil), replica.listeners...)
	replica.mu.Unlock()
	replica.deliverAll(listeners, next, ctx, pico3.ReplicatedStateDelivery{Kind: DeliveryHydrate, Sequence: sequence})
	return nil
}

// update applies the next gap-free batch.
func (replica *replicaCore) update(ctx context.Context, sequence int, ops []pico3.Op, valid func() bool) error {
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
	next, err := pico3.ApplyImmutable(replica.value, ops)
	if err != nil {
		replica.clearLocked()
		replica.mu.Unlock()
		return err
	}
	replica.value, replica.sequence = next, sequence
	listeners := append([]*replicaListener(nil), replica.listeners...)
	replica.mu.Unlock()
	replica.deliverAll(listeners, next, ctx, pico3.ReplicatedStateDelivery{Kind: DeliveryUpdate, Sequence: sequence})
	return nil
}

func (replica *replicaCore) clear() {
	replica.mu.Lock()
	defer replica.mu.Unlock()
	replica.clearLocked()
}

func (replica *replicaCore) clearLocked() {
	replica.value, replica.sequence, replica.hydrated = nil, 0, false
}

func (replica *replicaCore) deliverAll(listeners []*replicaListener, value pico3.JsonValue, ctx context.Context, delivery pico3.ReplicatedStateDelivery) {
	for _, entry := range listeners {
		replica.deliver(entry, value, ctx, delivery)
	}
}

func (replica *replicaCore) deliver(entry *replicaListener, value pico3.JsonValue, ctx context.Context, delivery pico3.ReplicatedStateDelivery) {
	if err := callValue(entry.listener, value, ctx, delivery); err != nil {
		replica.reportError(err)
	}
}

// ReplicatedStateReplica is a consumer's guarded handle to a replicated state
// member of a remote service. Like upstream's member view, each Value/Load/
// Subscribe checks the holder's access (binding disposal, facet revocation,
// closed keyed observation). It is untyped; use TypedReplica for a typed view.
type ReplicatedStateReplica struct {
	core   *replicaCore
	access func() error
}

// Load returns the current JSON value and whether the replica is hydrated, or
// the access error. The value is immutable by contract.
func (replica *ReplicatedStateReplica) Load() (pico3.JsonValue, bool, error) {
	if replica.access != nil {
		if err := replica.access(); err != nil {
			return nil, false, err
		}
	}
	value, hydrated := replica.core.snapshotValue()
	return value, hydrated, nil
}

// Value is Load for callers that treat revocation as a contract violation:
// an access failure panics with its error (upstream's value property throws)
// rather than reading as unhydrated. Use Load to handle it as an error.
func (replica *ReplicatedStateReplica) Value() (pico3.JsonValue, bool) {
	value, hydrated, err := replica.Load()
	if err != nil {
		panic(err)
	}
	return value, hydrated
}

// Subscribe registers listener after an access check; a hydrated replica
// delivers its retained value immediately as kind "hydrate".
func (replica *ReplicatedStateReplica) Subscribe(listener func(pico3.JsonValue, context.Context, pico3.ReplicatedStateDelivery) error) (func(), error) {
	if replica.access != nil {
		if err := replica.access(); err != nil {
			return nil, err
		}
	}
	return replica.core.subscribe(listener), nil
}

// ReplicaOf is a typed read view of a replica. It implements
// pico3.ReplicatedStateOf[T]; Value returns the zero T before hydration (use
// Load to distinguish).
type ReplicaOf[T any] struct {
	replica *ReplicatedStateReplica
}

var _ pico3.ReplicatedStateOf[map[string]any] = ReplicaOf[map[string]any]{}

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

// Value returns a detached decoded value, or the zero T before hydration
// (upstream undefined). An access or decoding failure panics with its error
// instead of reading as an empty value; use Load to handle it.
func (view ReplicaOf[T]) Value() T {
	decoded, _, err := view.Load()
	if err != nil {
		panic(err)
	}
	return decoded
}

// Subscribe decodes each delivery into a detached T; it fails only the
// access check.
func (view ReplicaOf[T]) Subscribe(listener func(T, context.Context, pico3.ReplicatedStateDelivery)) (func(), error) {
	return view.replica.Subscribe(typedListener(listener))
}
