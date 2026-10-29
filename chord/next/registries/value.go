package registries

import (
	"context"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/registries/value.ts (registry values, slice 8 §1 "Reading", S2, S3): a registry's
// composed value as the readers of one version see it, a native source. The provider's version reads the composition
// itself; an older one reads it mapped back with `accepts[old].value`, once per composition, kept only while someone
// reads or follows it; a version the provider cannot serve fails `contract_mismatch`. Its readers (attempts that
// declared it) keep the mapping; its waiters (attempts parked on it, a waiting `host.registry`) are woken at every
// change of whether it is usable, and of its failure while it is not (dependency model §9). Also the typed
// RegistryValue (registry.ts) over it.

// RegistryWaiter is who waits for a registry's value (a parked attempt, a `host.registry`): told when it may have
// changed.
type RegistryWaiter interface {
	// DependencyMoved is the value possibly changing (inside its batch: it only records).
	DependencyMoved()
	// Closed is the value closing with its host: the wait ends with err (`released/closed`).
	Closed(err *data.ChordError)
}

// sameError is whether two failures say the same: the same error, or ChordErrors alike (a version mismatch is made per
// read).
func sameError(a, b error) bool {
	if state.Identical(a, b) {
		return true
	}
	left, leftOK := data.IsChordError(a)
	right, rightOK := data.IsChordError(b)
	return leftOK && rightOK && left.Code == right.Code && left.Reason == right.Reason && left.Message == right.Message
}

// mapping is the last good mapping for an older version, the composition it was made from, and the contributions that
// composition was built from (kept only while all are in the table, decision 5); hasContributions is false when it was
// not built from a good composition record.
type mapping struct {
	from             any
	value            any
	contributions    []*RegistryContribution
	hasContributions bool
}

// mapFailure is a failed mapping of composition from, until another composition maps.
type mapFailure struct {
	from any
	err  error
}

// told is what a value's waiters were last told: usable (a value, or a failure with a last good one), or the error it
// has none for.
type told struct {
	usable bool
	err    error
}

// waitToken is one waiter's registration: a token of its own, until released.
type waitToken struct{ waiter RegistryWaiter }

// readToken is one reader's registration: a token of its own (one object may read twice), until released.
type readToken struct{ _ byte }

// RegistryValueSource is one version's view of a registry's composed value. While the host is open it reads its
// record; once the host closed it keeps its last start and lets the record go, so a value an app keeps keeps no closed
// host.
type RegistryValueSource struct {
	state.FollowedSource[any]
	Name    string
	Version int
	record  *RegistryRecord
	// last is its last start, kept once the host closed (record nil).
	last    state.NativeStart[any]
	readers map[*readToken]struct{}
	waiters []*waitToken
	told    *told
	mapped  *mapping
	failure *mapFailure
}

var (
	_ state.FollowedHooks[any] = (*RegistryValueSource)(nil)
	_ state.NativeSourceHandle = (*RegistryValueSource)(nil)
)

// NewRegistryValueSource makes the value of record's registry for readers of version.
func NewRegistryValueSource(record *RegistryRecord, version int, batch *state.Batch) *RegistryValueSource {
	source := &RegistryValueSource{Name: record.Name, Version: version, record: record, readers: map[*readToken]struct{}{}}
	source.Init(batch, source)
	return source
}

// Record is its registry's record while the host is open; nil once it closed.
func (v *RegistryValueSource) Record() *RegistryRecord { return v.record }

// Value is the composition (or its mapping) at its last good value. Upstream's getter throws the failure when there is
// none: Go panics with that error, the form a getter without an error result has.
func (v *RegistryValueSource) Value() any {
	start, err := v.start()
	if err != nil {
		panic(err)
	}
	if start.Kind == state.StartValue {
		return start.Value
	}
	if !start.HasLast {
		panic(start.Error)
	}
	return start.Last
}

// Start is its composition (mapped for an older version), composed now if its table changed; a change of whether it is
// usable, or of its failure while it is not, wakes its waiters. A read while its registry composes (a cycle) is a
// failure without a last value, where upstream throws.
func (v *RegistryValueSource) Start() state.NativeStart[any] {
	start, err := v.start()
	if err != nil {
		return state.NativeStart[any]{Kind: state.StartFailed, Error: err}
	}
	return start
}

// Current is its composition (or mapping) now; it fails while it is failed, though Value keeps the last good one.
func (v *RegistryValueSource) Current() (any, error) {
	start, err := v.start()
	if err != nil {
		return nil, err
	}
	if start.Kind == state.StartFailed {
		return nil, start.Error
	}
	return start.Value, nil
}

// Watched is whether it is followed: while someone watches or observes it, and while waiters wait on it.
func (v *RegistryValueSource) Watched() bool {
	return v.FollowedSource.Watched() || len(v.waiters) > 0
}

// Read registers a reader (an attempt that declared it) until the returned release: its mapping is kept meanwhile.
func (v *RegistryValueSource) Read() (release func()) {
	token := &readToken{}
	v.readers[token] = struct{}{}
	return func() {
		delete(v.readers, token)
		if len(v.readers) == 0 && !v.FollowedSource.Watched() {
			v.dropMapping()
		}
	}
}

// Wait registers waiter (a parked attempt's object, a waiting `host.registry`) until the returned release: it is told
// of every change of whether this value is usable (inside the batch that made it: it only records).
func (v *RegistryValueSource) Wait(waiter RegistryWaiter) (release func()) {
	token := &waitToken{waiter: waiter}
	if len(v.waiters) == 0 {
		v.told = nil
	}
	v.waiters = append(v.waiters, token)
	return func() {
		at := slices.Index(v.waiters, token)
		if at < 0 {
			return
		}
		v.waiters = slices.Concat(v.waiters[:at], v.waiters[at+1:])
		if len(v.waiters) == 0 {
			v.told = nil
		}
	}
}

// Recomposed is the registry's table changing (inside a batch): its dependents are marked, its followers get the next
// frame.
func (v *RegistryValueSource) Recomposed() { v.Changed() }

// Relisted is the registry's table changing again while not composed since (inside a batch): its dependents are marked
// already (none read it meanwhile, or it would be composed); its followers get the next frame. Marking stops here, so
// a cycle of registries through their values ends.
func (v *RegistryValueSource) Relisted() { v.List() }

// Close is the host closing: its watches and observers end, its readers and mapping go; it keeps its last start only.
func (v *RegistryValueSource) Close() {
	last, err := v.start()
	if err != nil {
		last = state.NativeStart[any]{Kind: state.StartFailed, Error: err}
	}
	v.record, v.last = nil, last
	clear(v.readers)
	waiters := v.waiters
	v.waiters = nil
	closed := data.NewChordError(data.ChordErrorInit{
		Code:    data.CodeReleased,
		Reason:  data.ReasonClosed,
		Message: fmt.Sprintf("registry %s: closed", v.Name),
	}, nil)
	for _, token := range waiters {
		token.waiter.Closed(closed)
	}
	v.told = nil
	v.dropMapping()
	v.End()
}

// Unfollowed is nobody following it: without readers, its mapping goes (computed again on the next read).
func (v *RegistryValueSource) Unfollowed() {
	if len(v.readers) == 0 {
		v.dropMapping()
	}
}

func (v *RegistryValueSource) start() (state.NativeStart[any], error) {
	record := v.record
	if record == nil {
		return v.last, nil
	}
	start, err := v.startOf(record)
	if err != nil {
		return state.NativeStart[any]{}, err
	}
	if len(v.waiters) > 0 {
		v.tell(start)
	}
	return start, nil
}

// tell wakes the waiters when start says something else than they were told (by usability, then by the error).
func (v *RegistryValueSource) tell(start state.NativeStart[any]) {
	previous := v.told
	usable := start.Kind == state.StartValue || start.HasLast
	if usable {
		if previous != nil && previous.usable {
			return
		}
	} else if previous != nil && !previous.usable && sameError(previous.err, start.Error) {
		return
	}
	if usable {
		v.told = &told{usable: true}
	} else {
		v.told = &told{err: start.Error}
	}
	// The first start of a wait wakes nobody: the waiter reads it itself.
	if previous == nil {
		return
	}
	for _, token := range slices.Clone(v.waiters) {
		token.waiter.DependencyMoved()
	}
}

func (v *RegistryValueSource) dropMapping() {
	v.mapped = nil
	v.failure = nil
}

func (v *RegistryValueSource) startOf(record *RegistryRecord) (state.NativeStart[any], error) {
	provided, ok := record.ProvidedVersion()
	if !ok || provided == v.Version {
		return record.Start()
	}
	var mapValue func(current any) (any, error)
	if provided > v.Version {
		mapValue = record.ValueAdapter(v.Version)
	}
	if mapValue == nil {
		message := fmt.Sprintf("registry %s: version %d is not served here (version %d is)", v.Name, v.Version, provided)
		err := data.NewChordError(data.ChordErrorInit{Code: data.CodeContractMismatch, Message: message}, nil)
		return state.NativeStart[any]{Kind: state.StartFailed, Error: err}, nil
	}
	current, err := record.Start()
	if err != nil {
		return state.NativeStart[any]{}, err
	}
	return v.mappedStart(record, current, mapValue), nil
}

// mappedStart is the composition current mapped back with mapValue, once per composition; a failing mapping fails this
// version only (reported once per episode), keeping its last good mapping; a composition mapped before heals it.
func (v *RegistryValueSource) mappedStart(record *RegistryRecord, current state.NativeStart[any], mapValue func(current any) (any, error)) state.NativeStart[any] {
	if current.Kind == state.StartFailed && !current.HasLast {
		return current
	}
	from := current.Value
	if current.Kind == state.StartFailed {
		from = current.Last
	}
	// `undefined` is a composition too: the records, not their fields, say whether there is one.
	// A mapping built from a contribution that left is no last good value any more (decision 5).
	if v.mapped != nil && v.mapped.hasContributions && !record.InTable(v.mapped.contributions) {
		v.mapped = nil
	}
	mapped, failure := v.mapped, v.failure
	if mapped != nil && state.Identical(mapped.from, from) {
		v.failure = nil
	} else if failure == nil || !state.Identical(failure.from, from) {
		value, err := record.Adapt(func() (any, error) { return mapValue(from) })
		if err == nil {
			contributions, ok := record.ComposedFrom()
			v.mapped = &mapping{from: from, value: value, contributions: contributions, hasContributions: ok}
			v.failure = nil
		} else {
			if failure == nil {
				record.Failed(err)
			}
			v.failure = &mapFailure{from: from, err: err}
		}
	}
	start := state.NativeStart[any]{}
	if v.mapped != nil {
		start.HasLast, start.Last = true, v.mapped.value
	}
	switch {
	case v.failure != nil:
		start.Kind, start.Error = state.StartFailed, v.failure.err
	case current.Kind == state.StartFailed:
		start.Kind, start.Error = state.StartFailed, current.Error
	default:
		start.Kind, start.Value = state.StartValue, v.mapped.value
	}
	return start
}

// typedValue is a RegistryValue[V] over the value source: upstream's cast of `RegistryValueSource` to
// `RegistryValue<V>`.
type typedValue[V any] struct{ source *RegistryValueSource }

// NewRegistryValue is source as a RegistryValue[V], the type a reader of a Registry[C, V] sees. Values cross as `any`
// and are asserted to V; a composed value that is not a V is a programming error of the registry's declaration.
func NewRegistryValue[V any](source *RegistryValueSource) RegistryValue[V] {
	return typedValue[V]{source: source}
}

// Value is the last good composition; it panics with the failure when there is none, as RegistryValueSource.Value.
func (typed typedValue[V]) Value() V {
	value, err := assertAs[V](typed.source.Value(), "registry "+typed.source.Name)
	if err != nil {
		panic(err)
	}
	return value
}

// Watch is a native watch of its compositions.
func (typed typedValue[V]) Watch(ctx context.Context) (chord.NativeWatchHandle[V], error) {
	handle, err := typed.source.Watch(ctx)
	if err != nil {
		return nil, err
	}
	return typedHandle[V]{handle: handle, name: typed.source.Name}, nil
}

// Observe attaches observer to its compositions, as RegistryValue.Observe.
func (typed typedValue[V]) Observe(observer state.NativeObserver[V], ctx context.Context) (state.NativeObservation[V], error) {
	observation, err := typed.source.Observe(typedObserver[V]{observer: observer, name: typed.source.Name}, ctx)
	if err != nil {
		return state.NativeObservation[V]{}, err
	}
	value, err := assertAs[V](observation.Value, "registry "+typed.source.Name)
	if err != nil {
		observation.Detach()
		return state.NativeObservation[V]{}, err
	}
	return state.NativeObservation[V]{Value: value, Detach: observation.Detach}, nil
}

// typedObserver is a NativeObserver[V] seen as one of `any`.
type typedObserver[V any] struct {
	observer state.NativeObserver[V]
	name     string
}

func (typed typedObserver[V]) Advance(call chord.NativeWatchCall[any], ctx context.Context) {
	typed.observer.Advance(typedCall[V](call, typed.name), ctx)
}

func (typed typedObserver[V]) End(end chord.WatchEnd) { typed.observer.End(end) }

// typedCall is call with its value asserted to V; one that is not a V is a failed call with that error.
func typedCall[V any](call chord.NativeWatchCall[any], name string) chord.NativeWatchCall[V] {
	if call.Kind != chord.WatchCallValue {
		return chord.NativeWatchCall[V]{Kind: call.Kind, Error: call.Error}
	}
	value, err := assertAs[V](call.Value, "registry "+name)
	if err != nil {
		return chord.NativeWatchCall[V]{Kind: chord.WatchCallFailed, Error: err}
	}
	return chord.NativeWatchCall[V]{Kind: chord.WatchCallValue, Value: value}
}

// typedHandle is a NativeWatchHandle[V] over one of `any`.
type typedHandle[V any] struct {
	handle chord.NativeWatchHandle[any]
	name   string
}

func (typed typedHandle[V]) Value() V {
	value, err := assertAs[V](typed.handle.Value(), "registry "+typed.name)
	if err != nil {
		panic(err)
	}
	return value
}

func (typed typedHandle[V]) Start(listener func(ctx context.Context, call chord.NativeWatchCall[V]) error) {
	typed.handle.Start(func(ctx context.Context, call chord.NativeWatchCall[any]) error {
		return listener(ctx, typedCall[V](call, typed.name))
	})
}

func (typed typedHandle[V]) Stop() (chord.WatchEnd, error) { return typed.handle.Stop() }
func (typed typedHandle[V]) Closed() <-chan struct{}       { return typed.handle.Closed() }
func (typed typedHandle[V]) End() chord.WatchEnd           { return typed.handle.End() }
