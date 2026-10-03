package delta

import (
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Object is a draft handle of a JSON object. It is usable only while its
// change is open; any later use panics with ErrRevoked. Containers read
// through it are returned as *Object or *Array handles, numbers as float64.
type Object struct{ n *node }

// Array is a draft handle of a JSON array with the same lifetime as Object.
// Held handles follow elements through reindexing.
type Array struct{ n *node }

func handle(stored any) any {
	kid, ok := stored.(*node)
	if !ok {
		return stored
	}
	if kid.isArray() {
		return &Array{n: kid}
	}
	return &Object{n: kid}
}

func (n *node) lock() func() {
	n.ctx.mu.Lock()
	if n.ctx.status != statusOpen || n.ctx.root == nil {
		n.ctx.mu.Unlock()
		panic(ErrRevoked)
	}
	return n.ctx.mu.Unlock
}

// place validates and clones a placement while the draft lock is held. A
// draft handle of the same change is materialized without relocking.
func (n *node) place(value any) (any, error) {
	return clonePlacementHeld(value, n.ctx)
}

// Get returns the value at key, or nil when absent.
func (object *Object) Get(key string) any {
	value, _ := object.Lookup(key)
	return value
}

// Lookup returns the value at key and whether it is present.
func (object *Object) Lookup(key string) (any, bool) {
	defer object.n.lock()()
	value, ok := object.n.objectLookup(key)
	return handle(value), ok
}

// Has reports whether key is present.
func (object *Object) Has(key string) bool {
	_, ok := object.Lookup(key)
	return ok
}

// Object returns the object handle at key, or nil when the value is not an object.
func (object *Object) Object(key string) *Object {
	result, _ := object.Get(key).(*Object)
	return result
}

// Array returns the array handle at key, or nil when the value is not an array.
func (object *Object) Array(key string) *Array {
	result, _ := object.Get(key).(*Array)
	return result
}

// Keys returns the present keys in JavaScript's own-key order: integer-like keys ascending, then the other keys. The
// other keys are the base keys in sorted order (a Go map has no insertion order), then keys added by this change in
// write order; a key deleted and added again counts as added.
func (object *Object) Keys() []string {
	defer object.n.lock()()
	n := object.n
	base := n.base.(map[string]any)
	keys := make([]string, 0, len(base)+len(n.order))
	for _, key := range slices.Sorted(maps.Keys(base)) {
		if !n.deletes[key] && !n.readded[key] {
			keys = append(keys, key)
		}
	}
	for _, key := range n.writtenKeys() {
		if _, inBase := base[key]; inBase && !n.readded[key] {
			continue
		}
		keys = append(keys, key)
	}
	return integerKeysFirst(keys)
}

// integerKeysFirst moves array-index keys to the front in ascending order and keeps the others in their order.
func integerKeysFirst(keys []string) []string {
	var indices, others []string
	for _, key := range keys {
		if isArrayIndexKey(key) {
			indices = append(indices, key)
			continue
		}
		others = append(others, key)
	}
	slices.SortFunc(indices, func(left, right string) int {
		if len(left) != len(right) {
			return len(left) - len(right)
		}
		return strings.Compare(left, right)
	})
	return append(indices, others...)
}

// isArrayIndexKey reports whether key is the canonical decimal form of an integer in [0, 2^32-2].
func isArrayIndexKey(key string) bool {
	if key == "0" {
		return true
	}
	if key == "" || key[0] == '0' || len(key) > 10 {
		return false
	}
	for _, digit := range key {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	number, err := strconv.ParseUint(key, 10, 64)
	return err == nil && number <= 1<<32-2
}

// Len returns the number of present keys.
func (object *Object) Len() int { return len(object.Keys()) }

// Set places a validated clone of value at key. A value that is not strict
// JSON returns an error and leaves the draft unchanged.
func (object *Object) Set(key string, value any) error {
	defer object.n.lock()()
	stored, err := object.n.place(value)
	if err != nil {
		return err
	}
	if object.n.attached() {
		object.n.objectSet(key, stored)
	}
	return nil
}

// Delete removes key; it is upstream's `draft.key = undefined` or `delete`.
func (object *Object) Delete(key string) {
	defer object.n.lock()()
	if object.n.attached() {
		object.n.objectDelete(key)
	}
}

// Snapshot returns a detached copy of the draft's present content. Changing
// the copy never changes the draft, its pending placements, or the base
// revision, as a value read out of upstream's Proxy draft cannot write through.
func (object *Object) Snapshot() map[string]any {
	defer object.n.lock()()
	value, _ := copyTrusted(object.n.current()).(map[string]any)
	return value
}

// Len returns the array length.
func (array *Array) Len() int {
	defer array.n.lock()()
	array.n.ensureEntries()
	return len(array.n.entries)
}

// Get returns the element at index, or nil when out of range.
func (array *Array) Get(index int) any {
	defer array.n.lock()()
	n := array.n
	n.ensureEntries()
	if index < 0 || index >= len(n.entries) {
		return nil
	}
	return handle(n.entryValue(n.entries[index]))
}

// Object returns the object handle at index, or nil.
func (array *Array) Object(index int) *Object {
	result, _ := array.Get(index).(*Object)
	return result
}

// Array returns the array handle at index, or nil.
func (array *Array) Array(index int) *Array {
	result, _ := array.Get(index).(*Array)
	return result
}

// Set places a validated clone of value at index; index may append one past
// the end.
func (array *Array) Set(index int, value any) error {
	defer array.n.lock()()
	n := array.n
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
		e.value = n.wrap(stored, "", e)
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
	entries := make([]*entry, 0, len(n.entries)-remove+len(inserted))
	entries = append(entries, n.entries[:start]...)
	entries = append(entries, inserted...)
	n.entries = append(entries, n.entries[start+remove:]...)
	if remove > 0 || len(inserted) > 0 {
		n.structural = true
		n.markChanged()
	}
	return removed
}

// Push appends validated clones and returns the new length. When any value
// is not strict JSON nothing is inserted.
func (array *Array) Push(values ...any) (int, error) {
	defer array.n.lock()()
	n := array.n
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
func (array *Array) Unshift(values ...any) (int, error) {
	defer array.n.lock()()
	n := array.n
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
func (array *Array) Pop() any {
	defer array.n.lock()()
	n := array.n
	n.ensureEntries()
	if len(n.entries) == 0 || !n.attached() {
		return nil
	}
	return copyTrusted(n.splice(len(n.entries)-1, 1, nil)[0])
}

// Shift removes and returns a detached copy of the first element's present value.
func (array *Array) Shift() any {
	defer array.n.lock()()
	n := array.n
	n.ensureEntries()
	if len(n.entries) == 0 || !n.attached() {
		return nil
	}
	return copyTrusted(n.splice(0, 1, nil)[0])
}

// Splice removes remove elements at start, inserts validated clones of
// items, and returns detached copies of the removed present values. A removed
// value never shares a container with the base revision, as a value removed
// from upstream's Proxy draft cannot write through.
func (array *Array) Splice(start, remove int, items ...any) ([]any, error) {
	defer array.n.lock()()
	n := array.n
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
		removed[index] = copyTrusted(value)
	}
	return removed, nil
}

// SetLen grows the array with nulls or shrinks it.
func (array *Array) SetLen(length int) {
	defer array.n.lock()()
	n := array.n
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

// Reverse reverses the elements in place.
func (array *Array) Reverse() {
	defer array.n.lock()()
	n := array.n
	n.ensureEntries()
	if !n.attached() || len(n.entries) < 2 {
		return
	}
	slices.Reverse(n.entries)
	n.structural = true
	n.markChanged()
}

// clampIndex is tracker.ts clampIndex: a negative index counts from the end, and the result lies in [0, length].
func clampIndex(index, length int) int {
	if index < 0 {
		return max(length+index, 0)
	}
	return min(index, length)
}

// Fill replaces the elements in [start, end) with independent validated clones of value, taken when Fill is called.
// A negative index counts from the end. As tracker.ts fill, an empty range places nothing, so value is validated only
// when the range is not empty.
func (array *Array) Fill(value any, start, end int) error {
	defer array.n.lock()()
	n := array.n
	n.ensureEntries()
	start, end = clampIndex(start, len(n.entries)), clampIndex(end, len(n.entries))
	if end <= start {
		return nil
	}
	stored, err := n.place(value)
	if err != nil {
		return err
	}
	if !n.attached() {
		return nil
	}
	items := make([]any, end-start)
	for index := range items {
		if items[index], err = clonePlacementHeld(stored, n.ctx); err != nil {
			return err
		}
	}
	n.splice(start, end-start, items)
	return nil
}

// CopyWithin copies the elements in [start, end) over the elements from target, by value, as Array.prototype.copyWithin.
// A negative index counts from the end.
func (array *Array) CopyWithin(target, start, end int) error {
	defer array.n.lock()()
	n := array.n
	n.ensureEntries()
	if !n.attached() {
		return nil
	}
	length := len(n.entries)
	target, start, end = clampIndex(target, length), clampIndex(start, length), clampIndex(end, length)
	count := min(max(end-start, 0), length-target)
	items := make([]any, count)
	for offset := range items {
		cloned, err := clonePlacementHeld(storedCurrent(n.entryValue(n.entries[start+offset])), n.ctx)
		if err != nil {
			return err
		}
		items[offset] = cloned
	}
	n.splice(target, count, items)
	return nil
}

// overrideSnapshot is an array slot's element override at the start of a sort.
type overrideSnapshot struct {
	set   bool
	value any
}

// Sort stably orders the elements by less. less receives each element as a draft handle, or the value itself for a
// primitive, and may edit the draft, including the array: element writes made through the handles stay, while the
// result of the sort replaces the first min(length at start, length at end) slots and so overwrites slot writes and
// insertions the comparator made there. An element the comparator left in two slots becomes an independent clone in
// the later one.
func (array *Array) Sort(less func(a, b any) bool) {
	n := array.n
	unlock := n.lock()
	n.ensureEntries()
	if !n.attached() || len(n.entries) < 2 {
		unlock()
		return
	}
	started := slices.Clone(n.entries)
	values := make(map[*entry]any, len(started))
	snapshots := make(map[*entry]overrideSnapshot, len(started))
	for _, e := range started {
		values[e] = handle(n.entryValue(e))
		snapshots[e] = overrideSnapshot{set: e.set, value: e.value}
	}
	unlock()

	sorted := slices.Clone(started)
	sort.SliceStable(sorted, func(i, j int) bool { return less(values[sorted[i]], values[sorted[j]]) })

	defer n.lock()()
	if !n.attached() {
		return
	}
	reentrant := !slices.Equal(n.entries, started)
	for _, e := range sorted {
		e.set, e.value, e.live = snapshots[e].set, snapshots[e].value, true
	}
	covered := min(len(sorted), len(n.entries))
	if !reentrant && slices.Equal(sorted, started) {
		return
	}
	for _, e := range n.entries[:covered] {
		if _, sorting := snapshots[e]; !sorting {
			e.live = false
		}
	}
	entries := append(slices.Clone(sorted), n.entries[covered:]...)
	if reentrant {
		seen := make(map[*entry]bool, len(entries))
		for index, e := range entries {
			if !seen[e] {
				seen[e] = true
				continue
			}
			cloned, err := clonePlacementHeld(storedCurrent(n.entryValue(e)), n.ctx)
			if err != nil {
				panic(err) // tracked content is strict JSON by construction
			}
			entries[index] = n.newEntry(cloned)
		}
	}
	n.entries = entries
	n.structural = true
	n.markChanged()
}

// Snapshot returns a detached copy of the present content, as Object.Snapshot.
func (array *Array) Snapshot() []any {
	defer array.n.lock()()
	value, _ := copyTrusted(array.n.current()).([]any)
	return value
}
