package delta

import (
	"errors"
	"math"
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// Obj is a draft handle of a JSON object. It is usable only while its change
// is open; any later use panics with ErrRevoked. Containers read through it are
// returned as *Obj or *Arr handles, numbers as float64.
type Obj struct{ n *node }

// Arr is a draft handle of a JSON array with the same lifetime as Obj. Held
// handles follow elements through reindexing.
type Arr struct{ n *node }

// ErrNotJSON reports a placement that is not a value of the model.
// Read-only: assigned once at initialization.
var ErrNotJSON = errors.New("value is not strict JSON")

func handle(stored any) any {
	kid, ok := stored.(*node)
	if !ok {
		return stored
	}
	if kid.isArray() {
		return &Arr{n: kid}
	}
	return &Obj{n: kid}
}

func (n *node) open() { n.ctx.assertOpen() }

// place validates a placement and returns a detached copy: a draft handle of
// the same change is materialized from its current content.
func (n *node) place(value any) (any, error) { return clonePlacement(value) }

func clonePlacement(value any) (any, error) {
	switch v := value.(type) {
	case nil, bool, string:
		return v, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, ErrNotJSON
		}
		return v, nil
	case int:
		return float64(v), nil
	case *Obj:
		v.n.open()
		return clonePlacement(v.n.current())
	case *Arr:
		v.n.open()
		return clonePlacement(v.n.current())
	case *jv.Object:
		out := jv.NewObjectCap(v.Len())
		var err error
		v.Entries(func(k string, item any) bool {
			var c any
			if c, err = clonePlacement(item); err != nil {
				return false
			}
			out.Set(k, c)
			return true
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			c, err := clonePlacement(item)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	}
	return nil, ErrNotJSON
}

// Get returns the value at key, or nil when absent.
func (o *Obj) Get(key string) any {
	value, _ := o.Lookup(key)
	return value
}

// Lookup returns the value at key and whether it is present.
func (o *Obj) Lookup(key string) (any, bool) {
	o.n.open()
	value, ok := o.n.objectLookup(key)
	return handle(value), ok
}

// Has reports whether key is present.
func (o *Obj) Has(key string) bool {
	_, ok := o.Lookup(key)
	return ok
}

// Obj returns the object handle at key, or nil when the value is not an object.
func (o *Obj) Obj(key string) *Obj {
	result, _ := o.Get(key).(*Obj)
	return result
}

// Arr returns the array handle at key, or nil when the value is not an array.
func (o *Obj) Arr(key string) *Arr {
	result, _ := o.Get(key).(*Arr)
	return result
}

// Keys returns the present keys in JavaScript's own-key order: integer-like
// keys ascending, then the other keys in base order, then keys added by this
// change in write order; a key deleted and added again counts as added.
func (o *Obj) Keys() []string {
	o.n.open()
	n := o.n
	base := n.base.(*jv.Object)
	keys := make([]string, 0, base.Len()+len(n.order))
	base.Members(func(key string, _ any) bool {
		if !n.deletes[key] && !n.readded[key] {
			keys = append(keys, key)
		}
		return true
	})
	for _, key := range n.writtenKeys() {
		if base.Has(key) && !n.readded[key] {
			continue
		}
		keys = append(keys, key)
	}
	scratch := jv.NewObjectCap(len(keys))
	for _, k := range keys {
		scratch.Set(k, nil)
	}
	return slices.Clone(scratch.Keys())
}

// Len returns the number of present keys.
func (o *Obj) Len() int { return len(o.Keys()) }

// Set places a validated clone of value at key. A value that is not strict JSON
// returns an error and leaves the draft unchanged.
func (o *Obj) Set(key string, value any) error {
	o.n.open()
	stored, err := o.n.place(value)
	if err != nil {
		return err
	}
	if o.n.attached() {
		o.n.objectSet(key, stored)
	}
	return nil
}

// Delete removes key; it is upstream's `draft.key = undefined` or `delete`.
func (o *Obj) Delete(key string) {
	o.n.open()
	if o.n.attached() {
		o.n.objectDelete(key)
	}
}

// Snapshot returns a detached copy of the draft's present content.
func (o *Obj) Snapshot() *jv.Object {
	o.n.open()
	value, _ := jv.Clone(o.n.current()).(*jv.Object)
	return value
}

// Len returns the array length.
func (a *Arr) Len() int {
	a.n.open()
	a.n.ensureEntries()
	return len(a.n.entries)
}

// Get returns the element at index, or nil when out of range.
func (a *Arr) Get(index int) any {
	a.n.open()
	n := a.n
	n.ensureEntries()
	if index < 0 || index >= len(n.entries) {
		return nil
	}
	return handle(n.entryValue(n.entries[index]))
}

// Obj returns the object handle at index, or nil.
func (a *Arr) Obj(index int) *Obj {
	result, _ := a.Get(index).(*Obj)
	return result
}

// Arr returns the array handle at index, or nil.
func (a *Arr) Arr(index int) *Arr {
	result, _ := a.Get(index).(*Arr)
	return result
}

// ErrSparseArray reports an array write that would leave a hole.
// Read-only: assigned once at initialization.
var ErrSparseArray = errors.New("Overlay arrays cannot contain holes")

// Read-only: assigned once at initialization.
var errNotArrayIndex = errors.New("Only array indices and length can be written")

// Set places a validated clone of value at index; index may append one past the end.
func (a *Arr) Set(index int, value any) error {
	a.n.open()
	n := a.n
	n.ensureEntries()
	if index < 0 {
		return errNotArrayIndex
	}
	if index > len(n.entries) {
		return ErrSparseArray
	}
	stored, err := n.place(value)
	if err != nil {
		return err
	}
	if !n.attached() {
		return nil
	}
	if index == len(n.entries) {
		n.entries = append(n.entries, n.newEntry(stored))
		n.structural = true
	} else {
		e := n.entries[index]
		if !isContainer(stored) {
			// Writing the value a slot already holds changes nothing; writing a base element's own value back drops its override (tracker.ts setArrayIndex).
			if current := n.entryValue(e); !isContainer(current) && current == stored {
				return nil
			}
			if e.index >= 0 && n.base.([]any)[e.index] == stored {
				e.set, e.value, e.setSeq = false, nil, 0
				n.markChanged()
				return nil
			}
		}
		e.value = n.wrap(stored, "", e)
		if !e.set {
			n.ctx.writeSeq++
			e.setSeq = n.ctx.writeSeq
		}
		e.set = true
	}
	n.markChanged()
	return nil
}

func (n *node) placeAll(values []any) ([]any, error) {
	stored := make([]any, len(values))
	for index, value := range values {
		placed, err := n.place(value)
		if err != nil {
			return nil, err
		}
		stored[index] = placed
	}
	return stored, nil
}

// splice removes and inserts entries and returns the removed values.
func (n *node) splice(start, remove int, stored []any) []any {
	n.ensureEntries()
	length := len(n.entries)
	start = min(max(start, 0), length)
	remove = min(max(remove, 0), length-start)
	removed := make([]any, remove)
	for offset := range remove {
		e := n.entries[start+offset]
		removed[offset] = storedCurrent(n.entryValue(e))
		e.live = false
	}
	inserted := make([]*entry, len(stored))
	for index, value := range stored {
		inserted[index] = n.newEntry(value)
	}
	// A nil entry list means an untouched array (ensureEntries), so an emptied array keeps a non-nil empty list.
	n.entries = slices.Replace(n.entries, start, start+remove, inserted...)
	if remove > 0 || len(inserted) > 0 {
		n.structural = true
		n.markChanged()
	}
	return removed
}

// Push appends validated clones and returns the new length. When any value is
// not strict JSON nothing is inserted.
func (a *Arr) Push(values ...any) (int, error) {
	a.n.open()
	n := a.n
	n.ensureEntries()
	stored, err := n.placeAll(values)
	if err != nil {
		return len(n.entries), err
	}
	if n.attached() {
		n.splice(len(n.entries), 0, stored)
	}
	return len(n.entries), nil
}

// Unshift inserts validated clones at the front and returns the new length.
func (a *Arr) Unshift(values ...any) (int, error) {
	a.n.open()
	n := a.n
	n.ensureEntries()
	stored, err := n.placeAll(values)
	if err != nil {
		return len(n.entries), err
	}
	if n.attached() {
		n.splice(0, 0, stored)
	}
	return len(n.entries), nil
}

// Pop removes and returns a detached copy of the last element's present value.
func (a *Arr) Pop() any {
	a.n.open()
	n := a.n
	n.ensureEntries()
	if len(n.entries) == 0 || !n.attached() {
		return nil
	}
	return jv.Clone(n.splice(len(n.entries)-1, 1, nil)[0])
}

// Shift removes and returns a detached copy of the first element's present value.
func (a *Arr) Shift() any {
	a.n.open()
	n := a.n
	n.ensureEntries()
	if len(n.entries) == 0 || !n.attached() {
		return nil
	}
	return jv.Clone(n.splice(0, 1, nil)[0])
}

// Splice removes remove elements at start, inserts validated clones of items,
// and returns detached copies of the removed present values.
func (a *Arr) Splice(start, remove int, items ...any) ([]any, error) {
	a.n.open()
	n := a.n
	n.ensureEntries()
	stored, err := n.placeAll(items)
	if err != nil {
		return nil, err
	}
	if !n.attached() {
		return []any{}, nil
	}
	if start < 0 {
		start = max(len(n.entries)+start, 0)
	}
	removed := n.splice(start, remove, stored)
	for index, value := range removed {
		removed[index] = jv.Clone(value)
	}
	return removed, nil
}

// SetLen grows the array with nulls or shrinks it.
func (a *Arr) SetLen(length int) {
	a.n.open()
	n := a.n
	n.ensureEntries()
	if !n.attached() || length < 0 {
		return
	}
	if length < len(n.entries) {
		n.splice(length, len(n.entries)-length, nil)
		return
	}
	n.splice(len(n.entries), 0, make([]any, length-len(n.entries)))
}

// Snapshot returns a detached copy of the present content.
func (a *Arr) Snapshot() []any {
	a.n.open()
	value, _ := jv.Clone(a.n.current()).([]any)
	return value
}
