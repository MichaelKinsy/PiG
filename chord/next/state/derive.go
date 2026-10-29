package state

import (
	"context"
	"reflect"
	"runtime"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next/state/derive.ts (Derive, DeriveSource, DeriveInput, mapShared). Derives (§12): values
// computed from watchables, pulled when read and marked dirty by commits; and mapShared, which keeps unchanged elements
// identical so a derive's diff stays cheap.

// deriveKind is where a derive stands. deriveDirty: a source changed since the last compute (or there was none). A
// failed derive (its compute failed) fails with its error until a source it reads changes or is re-bound, then
// recomputes (self-healing, design §11); a detached one (its object ended) fails with its object's error for good.
type deriveKind uint8

const (
	deriveDirty deriveKind = iota
	deriveClean
	deriveFailed
	deriveDetached
)

// framedKind is what a derive's watches have: nothing recorded (Pi's undefined), no value (Pi's NO_VALUE: a failed
// derive's watches had none, so its next value frame replaces the value whole) or a value.
type framedKind uint8

const (
	framedNone framedKind = iota
	framedNoValue
	framedValue
)

var noOps = []delta.Op{}

// Derive is a derive: compute over its sources' values, recomputed on read when dirty, so each compute runs at most once
// per batch and after its sources. A commit only marks it; the batch recomputes it only when something watched depends
// on it. It is a Source and a Dependent; NewDerive makes it.
type Derive struct {
	Source
	compute func(values []any) (delta.JsonValue, error)
	// declared is the sources as declared, until Attach resolves them.
	declared []any
	sources  []DeriveInput
	kind     deriveKind
	// value is the clean value; err is the failure of a failed or detached derive.
	value delta.JsonValue
	err   error
	// framed is the value this derive's watches have, kept from its marking to its frame in the batch.
	framed    framedKind
	framedVal delta.JsonValue
	// retired is whether a source's document was retired in the current batch while this derive is watched (rule 21).
	retired bool
	// report reports a failure of its compute to its owner (Attach); once per failure episode, which a value ends.
	report  func(error)
	failing bool
	// good is its last good value, which a watch opened while it is failed starts at (hasGood false before the first).
	good    delta.JsonValue
	hasGood bool
}

// NewDerive returns a derive of the declared sources, resolved by Attach, whose values compute turns into its value.
func NewDerive(sources []any, compute func(values []any) (delta.JsonValue, error), batch *Batch) *Derive {
	d := &Derive{declared: sources, compute: compute}
	d.Init(batch, d)
	return d
}

// Value is its value, recomputed when dirty; a failed derive returns its failure, and a detached one its object's error.
func (d *Derive) Value() (delta.JsonValue, error) {
	switch d.kind {
	case deriveClean:
		return d.value, nil
	case deriveDirty:
		return d.recompute()
	default:
		return nil, d.err
	}
}

// Start is its value, or while failed its failure and last good value. A detached derive (it throws) is a failure with no
// last value, which no watch can start at.
func (d *Derive) Start() WatchStart {
	value, err := d.Value()
	if err == nil {
		return WatchStart{Kind: StartValue, Value: value}
	}
	if d.kind != deriveFailed {
		return WatchStart{Kind: StartFailed, Error: err}
	}
	return WatchStart{Kind: StartFailed, Error: err, HasLast: d.hasGood, Last: d.good}
}

// Attach starts reading its sources, as resolve maps the declared ones (at activation); the first read computes. report
// gets a failure of its compute, once per failure episode (a value ends it); a failing source's error is its source's to
// report. A resolve that fails fails Attach with its error.
func (d *Derive) Attach(resolve func(source any, index int) (DeriveInput, error), report func(err error)) error {
	d.report = report
	sources := make([]DeriveInput, len(d.declared))
	for i, declared := range d.declared {
		source, err := resolve(declared, i)
		if err != nil {
			return err
		}
		sources[i] = source
	}
	d.sources = sources
	d.declared = nil
	for _, source := range sources {
		if dependents := source.DependentSet(); dependents != nil {
			dependents.Add(d)
		}
	}
	return nil
}

// Detach is its object ended: no source keeps it, its watches end ended, reads return err (the object's).
func (d *Derive) Detach(err error) {
	for _, source := range d.sources {
		if dependents := source.DependentSet(); dependents != nil {
			dependents.Delete(d)
		}
	}
	d.sources = nil
	d.declared = nil
	d.kind, d.err = deriveDetached, err
	d.value = nil
	d.framed, d.framedVal = framedNone, nil
	d.EndWatches()
}

// Invalidate is a source changed: mark this derive and its dependents dirty, flags only; a failed one too, which
// recomputes on its next read and heals when its sources give a value it can compute. An already dirty derive has dirty
// dependents (every read recomputes its sources first), so marking stops there.
func (d *Derive) Invalidate() {
	if d.kind == deriveDirty || d.kind == deriveDetached {
		return
	}
	if d.Watched() {
		if d.kind == deriveClean {
			d.framed, d.framedVal = framedValue, d.value
		} else {
			d.framed, d.framedVal = framedNoValue, nil
		}
	}
	d.kind = deriveDirty
	d.Changed()
}

// SourceRetired is a source's document was retired in this batch (Source.RetiredInBatch): a watched derive's frame carries
// the flag, and the derives reading this one are told too.
func (d *Derive) SourceRetired(visited map[Dependent]struct{}) {
	// Once per derive per retirement: a diamond of derives is walked in its size, not once per path.
	if _, seen := visited[d]; seen {
		return
	}
	visited[d] = struct{}{}
	if d.Watched() {
		d.retired = true
	}
	for _, dependent := range d.dependents.Snapshot() {
		dependent.SourceRetired(visited)
	}
}

// Deliver delivers the frame: the diff from the watches' value, a retirement even without ops (rule 21). A compute that
// fails gives one failure call when the watches had a value (one per failure episode), the watches stay open; a
// retirement waits for the next value. Diffing cannot fail in Go: delta.DiffRevisions takes JSON, which Go values
// cannot violate the way an `undefined` property does, so the error is always nil.
func (d *Derive) Deliver(ctx context.Context) error {
	beforeKind, before := d.framed, d.framedVal
	retired := d.retired
	d.framed, d.framedVal = framedNone, nil
	after, err := d.Value()
	if err != nil {
		// Still failed since the last call: nothing new for the watches.
		if beforeKind != framedNoValue {
			d.FailWatches(err, ctx)
		}
		return nil
	}
	d.retired = false
	ops := noOps
	if beforeKind != framedValue || !Identical(after, before) {
		d.batch.Count(CounterDeriveDiffs)
		if beforeKind != framedValue {
			ops = []delta.Op{{"r", after}}
		} else {
			ops = delta.DiffRevisions(before, after)
		}
	}
	if len(ops) > 0 || retired {
		d.Publish(after, ops, retired, ctx)
	}
	return nil
}

func (d *Derive) recompute() (delta.JsonValue, error) {
	var value delta.JsonValue
	err := d.batch.Compute(func() error {
		d.batch.Count(CounterDeriveComputes)
		// A failing source returns its own error here, so a derive over a failed derive fails with the same error.
		values := make([]any, len(d.sources))
		for i, source := range d.sources {
			read, err := source.DeriveValue()
			if err != nil {
				d.kind, d.err = deriveFailed, err
				return err
			}
			values[i] = read
		}
		computed, err := d.compute(values)
		if err != nil {
			if !d.failing && d.report != nil {
				d.report(HandedOut(err))
			}
			d.failing = true
			d.kind, d.err = deriveFailed, err
			return err
		}
		d.failing = false
		d.kind, d.value = deriveClean, computed
		d.good, d.hasGood = computed, true
		value = computed
		return nil
	})
	return value, err
}

// MapShared maps an array calling f only for elements that are objects not seen before (by identity), returning the
// previous result for the others; other elements are mapped every time. An element is an object when its dynamic value
// is a non-nil pointer, a map, or a non-empty slice (JSON objects are *delta.JsonObject, arrays []any); numbers,
// strings, booleans, nil, structs, functions and empty slices have no identity of their own and are mapped each time.
//
// The cache is weak, as Pi's WeakMap: a dropped element takes its result along (a cleanup removes the entry once the
// element is collected). Go has no ephemeron: a result that holds its own element keeps it alive, and with it the entry.
func MapShared[E, R any](f func(element E) R) func(elements []E) []R {
	cache := &sharedResults[R]{results: map[sharedKey]R{}}
	return func(elements []E) []R {
		mapped := make([]R, len(elements))
		for i, element := range elements {
			key, ok := identityKey(element)
			if !ok {
				mapped[i] = f(element)
				continue
			}
			cache.mu.Lock()
			result, found := cache.results[key.key]
			cache.mu.Unlock()
			if found {
				mapped[i] = result
				continue
			}
			result = f(element)
			mapped[i] = result
			cache.mu.Lock()
			if _, raced := cache.results[key.key]; !raced {
				cache.results[key.key] = result
				// The cleanup closes over cache only. cache holds a pointer (its map), so it is never a tiny allocation:
				// runtime.AddCleanup refuses a cleanup whose closure points into the block of the element it is
				// attached to, and a bare sync.Mutex captured here shared a 16-byte tiny block with a small element.
				runtime.AddCleanup(key.target, cache.drop, key.key)
			}
			cache.mu.Unlock()
		}
		return mapped
	}
}

// sharedResults is MapShared's cache: the results by element identity.
type sharedResults[R any] struct {
	mu      sync.Mutex
	results map[sharedKey]R
}

// drop removes the entry of a collected element.
func (cache *sharedResults[R]) drop(dropped sharedKey) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	delete(cache.results, dropped)
}

// sharedKey is the identity of an element: its object, weakly, and for a slice its length.
type sharedKey struct {
	object weak.Pointer[byte]
	length int
}

type sharedIdentity struct {
	key    sharedKey
	target *byte
}

// identityKey is the identity of element when it is an object (see MapShared).
func identityKey[E any](element E) (sharedIdentity, bool) {
	value := reflect.ValueOf(any(element))
	if !value.IsValid() {
		return sharedIdentity{}, false
	}
	length := 0
	switch value.Kind() {
	case reflect.Pointer, reflect.Map:
	case reflect.Slice:
		if value.Len() == 0 {
			return sharedIdentity{}, false
		}
		length = value.Len()
	default:
		return sharedIdentity{}, false
	}
	pointer := value.UnsafePointer()
	if pointer == nil {
		return sharedIdentity{}, false
	}
	// The weak pointer and the cleanup need the object's address, not its type: the address is all that identifies it.
	target := (*byte)(pointer)
	return sharedIdentity{key: sharedKey{object: weak.Make(target), length: length}, target: target}, true
}
