package host

// Ports packages/chord/src/cell.ts: object-owned state sources (cells and derivations). A write recomputes every
// dependent derivation against the candidate value, validates, adopts, and only then queues notifications (cell first,
// then derivations) on the publish.go queue.
//
// Values are the chord/delta JSON representation (nil, bool, float64, string, []any, *delta.JsonObject). An object
// cell or derivation is a Readable[any]; a Cell[T] with another T is a typed view whose values must hold T.

import (
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// SourceOptions is cell.ts SourceOptions.
type SourceOptions struct {
	Dev    bool
	Report func(err error)
	// Exec runs one write (change or replace) as a single section of the owning host's loop; nil runs it in the caller.
	// Upstream's writes are synchronous on one thread, so methods and tasks that write from several goroutines must not
	// interleave a write's read of the current revision with another write's commit.
	Exec func(write func())
	// loop is the owning host's loop, whose publication queue delivers the source's notifications (publish.go); nil is
	// the shared queue of a source that belongs to no host.
	loop *loop
}

// WriteGuard is cell.ts WriteGuard: it checks that ctx may write an object's cells now and returns the refusal.
type WriteGuard func(ctx Ctx) error

var sequence atomic.Int64

// fanout is cell.ts Fanout: it fans out values and delta events; listener panics are reported, not raised in the
// writer.
type fanout[T any] struct {
	mu     sync.Mutex
	values map[*func(T)]struct{}
	events map[*func(StateEvent[T])]struct{}
	// order keeps Pi's Set insertion order for delivery.
	valueOrder []*func(T)
	eventOrder []*func(StateEvent[T])
	report     func(err error)
	loop       *loop
}

func newFanout[T any](report func(err error), loop *loop) *fanout[T] {
	return &fanout[T]{
		values: map[*func(T)]struct{}{},
		events: map[*func(StateEvent[T])]struct{}{},
		report: report,
		loop:   loop,
	}
}

func (f *fanout[T]) subscribe(listener func(T), current T) func() {
	key := &listener
	f.mu.Lock()
	f.values[key] = struct{}{}
	f.valueOrder = append(f.valueOrder, key)
	f.mu.Unlock()
	call(f.report, listener, current)
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.values[key]; ok {
			delete(f.values, key)
			f.valueOrder = slices.DeleteFunc(f.valueOrder, func(entry *func(T)) bool { return entry == key })
		}
	}
}

func (f *fanout[T]) subscribeEvents(listener func(StateEvent[T]), current T) func() {
	key := &listener
	f.mu.Lock()
	f.events[key] = struct{}{}
	f.eventOrder = append(f.eventOrder, key)
	f.mu.Unlock()
	call(f.report, listener, StateEvent[T]{Type: StateEventSnap, Value: current})
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.events[key]; ok {
			delete(f.events, key)
			f.eventOrder = slices.DeleteFunc(f.eventOrder, func(entry *func(StateEvent[T])) bool { return entry == key })
		}
	}
}

// emit queues delivery to the listeners subscribed now. A listener that subscribes later already got this revision in
// its snapshot; one that unsubscribes before delivery is skipped.
func (f *fanout[T]) emit(event StateEvent[T]) {
	f.mu.Lock()
	events := slices.Clone(f.eventOrder)
	values := slices.Clone(f.valueOrder)
	f.mu.Unlock()
	enqueue(f.loop, func() {
		for _, listener := range events {
			if f.has(func() bool { _, ok := f.events[listener]; return ok }) {
				call(f.report, *listener, event)
			}
		}
		for _, listener := range values {
			if f.has(func() bool { _, ok := f.values[listener]; return ok }) {
				call(f.report, *listener, event.Value)
			}
		}
	})
}

func (f *fanout[T]) has(check func() bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return check()
}

// call runs a listener; a panic is the listener's throw and goes to report.
func call[V any](report func(err error), listener func(V), value V) {
	defer func() {
		if recovered := recover(); recovered != nil {
			report(panicError(recovered))
		}
	}()
	listener(value)
}

func panicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("%v", recovered)
}

// sourceNode is what computeDependents and Derived need of an object source (Pi's `instanceof Source`).
type sourceNode interface {
	dependentSet() *dependents
}

// dependents is Source.dependents, Pi's Set<Derived<unknown>>.
type dependents struct {
	mu  sync.Mutex
	set map[*Derived]struct{}
}

func (d *dependents) add(derived *Derived) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.set == nil {
		d.set = map[*Derived]struct{}{}
	}
	d.set[derived] = struct{}{}
}

func (d *dependents) delete(derived *Derived) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.set, derived)
}

func (d *dependents) list() []*Derived {
	d.mu.Lock()
	defer d.mu.Unlock()
	list := make([]*Derived, 0, len(d.set))
	for derived := range d.set {
		list = append(list, derived)
	}
	return list
}

// Has reports whether derived follows this source.
func (d *dependents) Has(derived *Derived) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.set[derived]
	return ok
}

// Len is the number of derivations following this source.
func (d *dependents) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.set)
}

// Hydration is cell.ts Hydration: whether a state source has its value. A remote reference's member before its first
// snapshot has none, and neither has anything derived from or forwarding it. Sources that do not implement hydrating
// always have their value.
type Hydration struct {
	Hydrated bool
	// WhenHydrated resolves once hydrated and rejects if the source ends first. It runs on the host's loop.
	WhenHydrated func() *Promise[struct{}]
}

// hydrating is a state source that may lack its value (cell.ts HYDRATION): a nil Hydration is a source that has it.
type hydrating interface{ hydration() *Hydration }

// hydrationOf is cell.ts hydrationOf: the hydration of a readable, or nil for a source that always has its value.
func hydrationOf(value any) *Hydration {
	if source, ok := value.(hydrating); ok {
		return source.hydration()
	}
	return nil
}

// unhydrated is cell.ts unhydrated: the hydrations of sources that are not hydrated yet.
func unhydrated(sources ...any) []*Hydration {
	var pending []*Hydration
	for _, source := range sources {
		if hydration := hydrationOf(source); hydration != nil && !hydration.Hydrated {
			pending = append(pending, hydration)
		}
	}
	return pending
}

// Source is cell.ts Source, the abstract object-owned state source (cell or derivation). Cell and Derived embed it.
type Source[T any] struct {
	Key string
	Seq int64
	// Dependents are the derivations computed from this source (Pi's dependents Set).
	Dependents *dependents
	// Schemas are the schemas of remote state members currently exposing this source (checked per write in dev mode).
	Schemas []Schema
	options SourceOptions
	fanout  *fanout[T]
	value   func() T
	own     func() []Schema
}

func newSource[T any](key string, options SourceOptions, value func() T, own func() []Schema) Source[T] {
	return Source[T]{
		Key:        key,
		Seq:        sequence.Add(1),
		Dependents: &dependents{},
		options:    options,
		fanout:     newFanout[T](options.Report, options.loop),
		value:      value,
		own:        own,
	}
}

func (source *Source[T]) dependentSet() *dependents { return source.Dependents }

// Subscribe calls listener with the current value now and with every later value.
func (source *Source[T]) Subscribe(listener func(T)) func() {
	return source.fanout.subscribe(listener, source.value())
}

// Events calls listener with a snapshot now and with every later delta event.
func (source *Source[T]) Events(listener func(StateEvent[T])) func() {
	return source.fanout.subscribeEvents(listener, source.value())
}

// Validate is the per-write validation, dev mode only.
func (source *Source[T]) Validate(value any) error {
	if !source.options.Dev {
		return nil
	}
	for _, schema := range source.own() {
		if err := assertValid("state", "state "+source.Key, schema, value); err != nil {
			return err
		}
	}
	for _, schema := range source.Schemas {
		if err := assertValid("state", "state "+source.Key, schema, value); err != nil {
			return err
		}
	}
	return nil
}

// unbox strips the { v } box the tracker tracks from published ops: the tracker tracks containers, so a cell tracks
// { v: value }.
func unbox(ops []delta.Op, value any) []delta.Op {
	result := make([]delta.Op, 0, len(ops))
	replace := []delta.Op{{"r", value}}
	for _, op := range ops {
		verb := delta.Verb(op)
		full := delta.PathOf(op)
		if verb == "r" || len(full) == 0 {
			return replace
		}
		path := full[1:]
		switch verb {
		case "p":
			result = append(result, delta.Op{"p", path, op[2], op[3], op[4]})
		case "m":
			result = append(result, delta.Op{"m", path, op[2]})
		default:
			// s/d/a/t on the value itself replace it.
			if len(path) == 0 {
				return replace
			}
			if verb == "d" {
				result = append(result, delta.Op{"d", path})
			} else {
				result = append(result, delta.Op{verb, path, op[2]})
			}
		}
	}
	return result
}

// Cell is cell.ts Cell: a state cell holding any JSON value, validated against its schema at creation and (dev mode)
// on writes.
type Cell[T any] struct {
	Source[T]
	// Schema is replaced at a reload commit that changes the schema (state-version bump).
	Schema  Schema
	tracker *delta.Tracker
	guard   WriteGuard
	// initialized is false until Initialize for a cell declared with init: reads and writes fail not_ready.
	initialized bool
}

// NewCell is cell.ts new Cell(key, schema, initial, guard, options). initial is always validated against schema;
// ownership moves to the cell.
func NewCell[T any](key string, schema Schema, initial T, guard WriteGuard, options SourceOptions) (*Cell[T], error) {
	return newCell(key, schema, &initial, guard, options)
}

// NewCellAwaitingInit is cell.ts new Cell(key, schema, undefined, guard, options): a cell without a value until
// Initialize (a cell declared with init, filled before its object goes live).
func NewCellAwaitingInit[T any](key string, schema Schema, guard WriteGuard, options SourceOptions) (*Cell[T], error) {
	return newCell[T](key, schema, nil, guard, options)
}

// newCell is the Cell constructor: initial nil is cell.ts's `initial: undefined`.
func newCell[T any](key string, schema Schema, initial *T, guard WriteGuard, options SourceOptions) (*Cell[T], error) {
	cell := &Cell[T]{}
	cell.Source = newSource(key, options, cell.Value, func() []Schema { return []Schema{cell.Schema} })
	var value any
	if initial != nil {
		value = *initial
		if err := assertValid("state", "initial value of state "+key, schema, value); err != nil {
			return nil, err
		}
	}
	cell.Schema = schema
	cell.tracker = delta.Track(delta.JsonObjectOf("v", value))
	cell.guard = guard
	cell.initialized = initial != nil
	return cell, nil
}

// Initialize sets the first value of a cell declared with init (validated; ownership moves to the cell). It publishes
// nothing.
func (cell *Cell[T]) Initialize(value T) error {
	if cell.initialized {
		return fmt.Errorf("state %s is already initialized", cell.Key)
	}
	if err := assertValid("state", "initial value of state "+cell.Key, cell.Schema, value); err != nil {
		return err
	}
	cell.tracker = delta.Track(delta.JsonObjectOf("v", value))
	cell.initialized = true
	return nil
}

// Value is the current value. A cell declared with init throws (panics with) not_ready until it is initialized.
func (cell *Cell[T]) Value() T {
	if !cell.initialized {
		panic(notReadyInit(cell.Key))
	}
	return as[T](cell.tracker.Value().Value("v"))
}

func notReadyInit(key string) *ChordError {
	return NotReady("state " + key + " (its init has not run)")
}

func as[T any](value any) T {
	typed, _ := value.(T)
	return typed
}

// Change is cell.ts change(ctx, fn) with a draft-mutating fn (Pi's void-returning overload): only mutated paths are
// published. draft is the value's draft reference (*delta.Object or *delta.Array) for a container, or the value itself for
// a primitive. The draft is unusable after fn returns.
func (cell *Cell[T]) Change(ctx Ctx, fn func(draft any)) error {
	return cell.change(ctx, func(draft any) (any, bool) {
		fn(draft)
		return nil, false
	})
}

// exec runs one write as a single section (SourceOptions.Exec).
func (cell *Cell[T]) exec(write func() error) (err error) {
	if cell.options.Exec == nil {
		return write()
	}
	cell.options.Exec(func() { err = write() })
	return err
}

// ChangeValue is cell.ts change(ctx, fn) with a value-returning fn (`n => n + 1`). The returned value replaces the
// cell's value and is always validated against the schema, unless fn returns the draft itself.
func (cell *Cell[T]) ChangeValue(ctx Ctx, fn func(draft any) T) error {
	return cell.change(ctx, func(draft any) (any, bool) {
		return fn(draft), true
	})
}

func (cell *Cell[T]) change(ctx Ctx, fn func(draft any) (any, bool)) error {
	return cell.exec(func() error { return cell.changeNow(ctx, fn) })
}

func (cell *Cell[T]) changeNow(ctx Ctx, fn func(draft any) (any, bool)) (err error) {
	if err := cell.guard(ctx); err != nil {
		return err
	}
	if !cell.initialized {
		return notReadyInit(cell.Key)
	}
	change := cell.tracker.BeginChange()
	settled := false
	defer func() {
		// A panicking fn is Pi's throwing fn: the change is aborted and the panic continues.
		if !settled {
			change.Abort()
		}
	}()
	draft := change.State()
	current := draft.Get("v")
	result, returned := fn(current)
	if returned && !sameDraft(result, current) {
		if err := assertValid("state", "state "+cell.Key, cell.Schema, result); err != nil {
			settled = true
			change.Abort()
			return err
		}
		if err := draft.Set("v", result); err != nil {
			settled = true
			change.Abort()
			return err
		}
	}
	prepared, err := change.Prepare()
	settled = true
	if err != nil {
		change.Abort()
		return err
	}
	return cell.publish(prepared, StateEventOps, true, nil)
}

// sameDraft is Pi's `result === draft.v`: the same primitive, or the same object. JavaScript compares objects by identity, so a
// map, slice or function is the same only when it is the same one; Go's == would panic on those types.
func sameDraft(result, draft any) bool {
	if result == nil || draft == nil {
		return result == draft
	}
	resultType := reflect.TypeOf(result)
	if resultType != reflect.TypeOf(draft) {
		return false
	}
	if resultType.Comparable() {
		return result == draft
	}
	resultValue, draftValue := reflect.ValueOf(result), reflect.ValueOf(draft)
	switch resultValue.Kind() {
	case reflect.Map, reflect.Func:
		return resultValue.UnsafePointer() == draftValue.UnsafePointer()
	case reflect.Slice:
		return resultValue.UnsafePointer() == draftValue.UnsafePointer() && resultValue.Len() == draftValue.Len()
	}
	return false
}

// CellReplaceOptions is the options parameter of cell.ts Cell.replace.
type CellReplaceOptions struct {
	// Diff selects a structural diff (true) or a single r op (false); nil diffs when the new value shares containers
	// with the old one.
	Diff *bool
}

// Replace is cell.ts replace(ctx, value, options): it replaces the whole value. Ownership of value moves to the cell:
// never mutate it afterwards.
//
// Subscribers get a structural diff (delta.DiffRevisions) when Diff is true, or by default when the new value shares
// containers with the old one (a new immutable revision of the same state, e.g. mirrored from another store);
// otherwise a single r op. The resulting value is the same either way; only the ops differ.
func (cell *Cell[T]) Replace(ctx Ctx, value T, options CellReplaceOptions) error {
	return cell.exec(func() error { return cell.replaceNow(ctx, value, options) })
}

func (cell *Cell[T]) replaceNow(ctx Ctx, value T, options CellReplaceOptions) error {
	if err := cell.guard(ctx); err != nil {
		return err
	}
	if !cell.initialized {
		return notReadyInit(cell.Key)
	}
	before := cell.tracker.Value().Value("v")
	diff := false
	if options.Diff != nil {
		diff = *options.Diff
	} else {
		diff = sharesStructure(before, value)
	}
	var ops []delta.Op
	if diff {
		ops = delta.DiffRevisions(before, value)
	}
	return cell.publish(cell.tracker.PrepareReplace(delta.JsonObjectOf("v", value)), StateEventOps, true, ops)
}

// Reset is cell.ts reset(value, schema): it installs a migrated value and its schema (reload commit). The caller
// validated the value and the derivations over it against every schema that will apply, so nothing is checked here.
// Subscribers receive a reset.
func (cell *Cell[T]) Reset(value T, schema Schema) error {
	cell.Schema = schema
	return cell.publish(cell.tracker.PrepareReplace(delta.JsonObjectOf("v", value)), StateEventReset, false, nil)
}

// publish adopts this revision and every derived one, then queues all notifications (cell first, then derivations).
func (cell *Cell[T]) publish(prepared *delta.Prepared, kind StateEventType, validate bool, ops []delta.Op) error {
	if len(prepared.Ops()) == 0 && kind == StateEventOps {
		return cell.tracker.Adopt(prepared)
	}
	value := prepared.Value().Value("v")
	var candidates []dependentValue
	err := func() error {
		if validate {
			if err := cell.Validate(value); err != nil {
				return err
			}
		}
		var err error
		candidates, err = computeDependents(cell, any(cell), value, validate)
		return err
	}()
	if err != nil {
		prepared.Abort()
		return err
	}
	return holdPublications(cell.options.loop, func() error {
		if err := cell.tracker.Adopt(prepared); err != nil {
			return err
		}
		published := ops
		if len(published) == 0 {
			published = unbox(prepared.Ops(), value)
		}
		if kind == StateEventReset {
			cell.fanout.emit(StateEvent[T]{Type: kind, Value: as[T](value)})
		} else {
			cell.fanout.emit(StateEvent[T]{Type: kind, Ops: published, Value: as[T](value)})
		}
		for _, candidate := range candidates {
			candidate.derived.Install(candidate.value)
		}
		return nil
	})
}

// sharesStructure reports whether b reuses at least one container of a at the same key: a new revision of the same
// state.
func sharesStructure(a, b any) bool {
	if !isContainer(a) || !isContainer(b) || sameContainer(a, b) {
		return false
	}
	aObject, aIsObject := a.(*delta.JsonObject)
	switch typed := b.(type) {
	case *delta.JsonObject:
		if !aIsObject {
			return false
		}
		for key, child := range typed.All() {
			if isContainer(child) && sameContainer(aObject.Value(key), child) {
				return true
			}
		}
	case []any:
		aArray, ok := a.([]any)
		if !ok {
			return false
		}
		for index, child := range typed {
			if index < len(aArray) && isContainer(child) && sameContainer(aArray[index], child) {
				return true
			}
		}
	}
	return false
}

func isContainer(value any) bool {
	switch typed := value.(type) {
	case *delta.JsonObject:
		return typed != nil
	case []any:
		return typed != nil
	}
	return false
}

// sameContainer is JavaScript identity of two containers: the same object, or a slice of the same backing array and
// length.
func sameContainer(a, b any) bool {
	switch left := a.(type) {
	case *delta.JsonObject:
		right, ok := b.(*delta.JsonObject)
		return ok && left == right
	case []any:
		right, ok := b.([]any)
		return ok && len(left) == len(right) && unsafe.SliceData(left) == unsafe.SliceData(right) //nolint:gosec // G103: compares backing arrays, JavaScript's array identity
	}
	return false
}

type dependentValue struct {
	derived *Derived
	value   any
}

// computeDependents recomputes every transitive dependent of changed against candidate values and validates them, in
// creation order.
func computeDependents(changed sourceNode, key any, value any, validate bool) ([]dependentValue, error) {
	pending := map[*Derived]struct{}{}
	var visit func(source sourceNode)
	visit = func(source sourceNode) {
		for _, derived := range source.dependentSet().list() {
			if _, ok := pending[derived]; ok {
				continue
			}
			pending[derived] = struct{}{}
			visit(derived)
		}
	}
	visit(changed)
	ordered := make([]*Derived, 0, len(pending))
	for derived := range pending {
		ordered = append(ordered, derived)
	}
	slices.SortFunc(ordered, func(a, b *Derived) int { return int(a.Seq - b.Seq) })
	values := map[any]any{key: value}
	result := make([]dependentValue, 0, len(ordered))
	for _, derived := range ordered {
		next, err := derived.Compute(func(source Readable[any]) any {
			if candidate, ok := values[any(source)]; ok {
				return candidate
			}
			return source.Value()
		})
		if err != nil {
			return nil, err
		}
		if validate {
			if err := derived.Validate(next); err != nil {
				return nil, err
			}
		}
		values[any(derived)] = next
		result = append(result, dependentValue{derived: derived, value: next})
	}
	return result, nil
}

// Sources is cell.ts Sources: the readables a derivation is computed from.
type Sources = []Readable[any]

// Derived is cell.ts Derived: a synchronous derivation. Sources are object cells/derivations (recomputed atomically
// with the write) or other readables such as reference state members (recomputed when they publish). Subscribers receive
// diffs.
type Derived struct {
	Source[any]
	Sources  Sources
	mu       sync.Mutex
	fn       func(values ...any) any
	current  any
	computed bool
	external []func()
}

// NewDerived is cell.ts new Derived(key, sources, fn, options). fn panics where Pi's throws.
func NewDerived(key string, sources Sources, fn func(values ...any) any, options SourceOptions) (*Derived, error) {
	derived := &Derived{fn: fn}
	derived.Source = newSource(key, options, derived.Value, func() []Schema { return nil })
	for _, source := range sources {
		if source == nil {
			return nil, typeError("derive %s: sources must be readables", key)
		}
	}
	derived.Sources = slices.Clone(sources)
	return derived, nil
}

// Value is the current value (nil before Attach).
func (derived *Derived) Value() any {
	derived.mu.Lock()
	defer derived.mu.Unlock()
	return derived.current
}

// hydration is hydrated once every source is (a derivation over a remote member waits for its snapshot).
func (derived *Derived) hydration() *Hydration {
	sources := make([]any, len(derived.Sources))
	for index, source := range derived.Sources {
		sources[index] = source
	}
	pending := unhydrated(sources...)
	if len(pending) == 0 {
		return nil
	}
	return &Hydration{Hydrated: false, WhenHydrated: func() *Promise[struct{}] {
		waits := make([]*Promise[struct{}], len(pending))
		var loop *loop
		for index, hydration := range pending {
			waits[index] = hydration.WhenHydrated()
			loop = waits[index].loop
		}
		return promiseAll(loop, waits)
	}}
}

// Attach computes the first value (or takes it from precomputed, a reload's validated dry run, keyed by derivation)
// and starts following the sources (at install, never inside a factory).
func (derived *Derived) Attach(precomputed map[any]any) error {
	value, ok := precomputed[any(derived)]
	if !ok {
		var err error
		value, err = derived.Compute(func(source Readable[any]) any { return source.Value() })
		if err != nil {
			return err
		}
	}
	derived.mu.Lock()
	derived.current, derived.computed = value, true
	derived.mu.Unlock()
	for _, source := range derived.Sources {
		if node, ok := source.(sourceNode); ok {
			node.dependentSet().add(derived)
		} else {
			unsubscribe := source.Subscribe(func(any) { derived.Refresh() })
			derived.mu.Lock()
			derived.external = append(derived.external, unsubscribe)
			derived.mu.Unlock()
		}
	}
	return nil
}

// Detach stops following the sources.
func (derived *Derived) Detach() {
	for _, source := range derived.Sources {
		if node, ok := source.(sourceNode); ok {
			node.dependentSet().delete(derived)
		}
	}
	derived.mu.Lock()
	external := derived.external
	derived.external = nil
	derived.mu.Unlock()
	for _, unsubscribe := range external {
		unsubscribe()
	}
}

// Compute applies the derivation's function to the values read from the sources. A panicking fn is Pi's throwing fn and
// returns its error.
func (derived *Derived) Compute(read func(source Readable[any]) any) (result any, err error) {
	values := make([]any, len(derived.Sources))
	for index, source := range derived.Sources {
		values[index] = read(source)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result, err = nil, panicError(recovered)
		}
	}()
	return derived.fn(values...), nil
}

// Refresh recomputes and republishes, including dependents. Errors are reported (the source write already happened).
func (derived *Derived) Refresh() {
	next, err := derived.Compute(func(source Readable[any]) any { return source.Value() })
	var candidates []dependentValue
	if err == nil {
		candidates, err = computeDependents(derived, any(derived), next, true)
	}
	if err != nil {
		derived.options.Report(err)
		return
	}
	holdPublications(derived.options.loop, func() struct{} {
		derived.Install(next)
		for _, candidate := range candidates {
			candidate.derived.Install(candidate.value)
		}
		return struct{}{}
	})
}

// Install adopts next and publishes its diff from the previous value (a single r op before the first value).
func (derived *Derived) Install(next any) {
	derived.mu.Lock()
	before, computed := derived.current, derived.computed
	derived.current, derived.computed = next, true
	derived.mu.Unlock()
	var ops []delta.Op
	if !computed {
		ops = []delta.Op{{"r", next}}
	} else {
		ops = delta.DiffRevisions(before, next)
	}
	if len(ops) > 0 {
		derived.fanout.emit(StateEvent[any]{Type: StateEventOps, Ops: ops, Value: next})
	}
}

// IsSource is cell.ts isSource: whether value is an object cell or derivation.
func IsSource(value any) bool {
	_, ok := value.(sourceNode)
	return ok
}

// NotReady is cell.ts notReady: the error placeholders throw when used before activation finished.
func NotReady(what string) *ChordError {
	return NewChordError(CodeNotReady, what+" is not ready", ChordErrorOptions{})
}
