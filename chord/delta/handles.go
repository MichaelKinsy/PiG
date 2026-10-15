package delta

import (
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Object is a draft handle of a JSON object. It is usable only while its
// change is open; any later use panics with ErrRevoked. Containers read
// through it are returned as *Object or *Array handles, numbers as float64.
type Object struct{ ref nodeRef }

// Array is a draft handle of a JSON array with the same lifetime as Object.
// Held handles follow elements through reindexing.
type Array struct{ ref nodeRef }

// nodeRef reaches a draft node through its overlay, so a handle the caller keeps after the change settles holds no part of the draft or of a revision.
type nodeRef struct {
	ctx   *overlay
	index int
}

func handle(stored any) any {
	kid, ok := stored.(*node)
	if !ok {
		return stored
	}
	if kid.isArray() {
		return &Array{ref: kid.ctx.refHeld(kid)}
	}
	return &Object{ref: kid.ctx.refHeld(kid)}
}

// lock locks the handle's overlay and returns its node and the locked mutex, or panics with ErrRevoked once the change settled. Returning the mutex rather than its Unlock method value keeps the call allocation-free.
func (ref nodeRef) lock() (*node, *sync.Mutex) {
	ref.ctx.mu.Lock()
	if ref.ctx.status != statusOpen || ref.ctx.root == nil {
		ref.ctx.mu.Unlock()
		panic(ErrRevoked)
	}
	return ref.ctx.nodes[ref.index-1], &ref.ctx.mu
}

// placements validates and clones values for a placement into ref's draft before that draft is locked. Copying a handle locks the handle's draft; holding this draft's lock meanwhile would deadlock with another goroutine's change that is placing one of this draft's handles. A settled draft panics with ErrRevoked before anything is copied, as tracker.ts asserts the overlay writable before it copies.
func (ref nodeRef) placements(values ...any) ([]any, error) {
	_, mu := ref.lock()
	mu.Unlock()
	stored := make([]any, len(values))
	for index, value := range values {
		placed, err := clonePlacementHeld(value, nil)
		if err != nil {
			return nil, err
		}
		stored[index] = placed
	}
	return stored, nil
}

// placement is placements for one value.
func (ref nodeRef) placement(value any) (any, error) {
	_, mu := ref.lock()
	mu.Unlock()
	return clonePlacementHeld(value, nil)
}

// Get returns the value at key, or nil when absent.
func (object *Object) Get(key string) any {
	value, _ := object.Lookup(key)
	return value
}

// Lookup returns the value at key and whether it is present.
func (object *Object) Lookup(key string) (any, bool) {
	n, mu := object.ref.lock()
	defer mu.Unlock()
	value, ok := n.objectLookup(key)
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

// Keys returns the present keys in JavaScript's own-key order: array-index keys ascending, then the other keys in draft order: the base keys in their order, then keys added by this change in write order; a key deleted and added again counts as added.
func (object *Object) Keys() []string {
	n, mu := object.ref.lock()
	defer mu.Unlock()
	base := n.base.(*JsonObject)
	keys := make([]string, 0, base.Len()+len(n.order))
	for key := range base.All() {
		if !n.deletes[key] && !n.readded[key] {
			keys = append(keys, key)
		}
	}
	for _, key := range n.writtenKeys() {
		if base.Has(key) && !n.readded[key] {
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
	stored, err := object.ref.placement(value)
	if err != nil {
		return err
	}
	n, mu := object.ref.lock()
	defer mu.Unlock()
	if n.attached() {
		n.objectSet(key, stored)
	}
	return nil
}

// Delete removes key; it is upstream's `draft.key = undefined` or `delete`.
func (object *Object) Delete(key string) {
	n, mu := object.ref.lock()
	defer mu.Unlock()
	if n.attached() {
		n.objectDelete(key)
	}
}

// Snapshot returns a detached copy of the draft's present content. Changing
// the copy never changes the draft, its pending placements, or the base
// revision, as a value read out of upstream's Proxy draft cannot write through.
func (object *Object) Snapshot() *JsonObject {
	n, mu := object.ref.lock()
	defer mu.Unlock()
	value, _ := copyTrusted(n.current()).(*JsonObject)
	return value
}

// Len returns the array length.
func (array *Array) Len() int {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	return n.arrayLength()
}

// Get returns the element at index, or nil when out of range.
func (array *Array) Get(index int) any {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	if index < 0 || index >= n.arrayLength() {
		return nil
	}
	p, offset := n.arrayOverlay().locate(index)
	sourceIndex := p.sourceIndex(offset)
	value := n.slot(p, sourceIndex)
	if isContainer(value) {
		// A base container read for the first time gets the entry that holds its node.
		value = n.entryValue(n.slotEntry(p, sourceIndex))
	}
	return handle(value)
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
	if err := array.checkIndex(index); err != nil {
		return err
	}
	stored, err := array.ref.placement(value)
	if err != nil {
		return err
	}
	n, mu := array.ref.lock()
	defer mu.Unlock()
	length := n.arrayLength()
	if index > length {
		return ErrSparseArray
	}
	if !n.attached() {
		return nil
	}
	if index == length {
		n.replacePieceRange(length, 0, n.insertPiece([]any{stored}))
		return nil
	}
	p, offset := n.arrayOverlay().locate(index)
	sourceIndex := p.sourceIndex(offset)
	if !isContainer(stored) {
		// Writing the value a slot already holds changes nothing; writing a base element's own value back drops its override (tracker.ts setArrayIndex).
		if current := n.slot(p, sourceIndex); !isContainer(current) && current == stored {
			return nil
		}
		if p.isBase() && n.base.([]any)[sourceIndex] == stored {
			e := n.array.baseEntries.get(sourceIndex)
			e.set, e.value, e.setSeq = false, nil, 0
			n.markChanged()
			return nil
		}
	}
	e := n.slotEntry(p, sourceIndex)
	e.value = n.wrap(stored, "", e)
	if !e.set {
		n.ctx.writeSeq++
		e.setSeq = n.ctx.writeSeq
	}
	e.set = true
	n.markChanged()
	return nil
}

// checkIndex reports the index errors of Set, which tracker.ts raises before it copies the value.
func (array *Array) checkIndex(index int) error {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	if index < 0 {
		return errNotArrayIndex
	}
	if index > n.arrayLength() {
		return ErrSparseArray
	}
	return nil
}

// splice removes and inserts elements and returns the removed values.
func (n *node) splice(start, remove int, stored []any) []any {
	length := n.arrayLength()
	start = min(max(start, 0), length)
	remove = min(max(remove, 0), length-start)
	removed := make([]any, remove)
	for offset := range remove {
		removed[offset] = storedCurrent(n.valueAt(start + offset))
	}
	n.replacePieceRange(start, remove, n.insertPiece(stored))
	return removed
}

// Push appends validated clones and returns the new length. When any value
// is not strict JSON nothing is inserted.
func (array *Array) Push(values ...any) (int, error) {
	stored, err := array.ref.placements(values...)
	n, mu := array.ref.lock()
	defer mu.Unlock()
	if err != nil {
		return n.arrayLength(), err
	}
	if n.attached() {
		n.splice(n.arrayLength(), 0, stored)
	}
	return n.arrayLength(), nil
}

// Unshift inserts validated clones at the front and returns the new length.
func (array *Array) Unshift(values ...any) (int, error) {
	stored, err := array.ref.placements(values...)
	n, mu := array.ref.lock()
	defer mu.Unlock()
	if err != nil {
		return n.arrayLength(), err
	}
	if n.attached() {
		n.splice(0, 0, stored)
	}
	return n.arrayLength(), nil
}

// Pop removes and returns a detached copy of the last element's present value.
func (array *Array) Pop() any {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	length := n.arrayLength()
	if length == 0 || !n.attached() {
		return nil
	}
	return copyTrusted(n.splice(length-1, 1, nil)[0])
}

// Shift removes and returns a detached copy of the first element's present value.
func (array *Array) Shift() any {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	if n.arrayLength() == 0 || !n.attached() {
		return nil
	}
	return copyTrusted(n.splice(0, 1, nil)[0])
}

// Splice removes remove elements at start, inserts validated clones of
// items, and returns detached copies of the removed present values. A removed
// value never shares a container with the base revision, as a value removed
// from upstream's Proxy draft cannot write through.
func (array *Array) Splice(start, remove int, items ...any) ([]any, error) {
	stored, err := array.ref.placements(items...)
	if err != nil {
		return nil, err
	}
	n, mu := array.ref.lock()
	defer mu.Unlock()
	if !n.attached() {
		return []any{}, nil
	}
	if start < 0 {
		start = max(n.arrayLength()+start, 0)
	}
	removed := n.splice(start, remove, stored)
	for index, value := range removed {
		removed[index] = copyTrusted(value)
	}
	return removed, nil
}

// SetLen grows the array with nulls or shrinks it.
func (array *Array) SetLen(length int) {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	if !n.attached() || length < 0 {
		return
	}
	current := n.arrayLength()
	if length < current {
		n.splice(length, current-length, nil)
		return
	}
	n.splice(current, 0, make([]any, length-current))
}

// Reverse reverses the elements in place: tracker.ts reverse, which reverses the pieces and flips each.
func (array *Array) Reverse() {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	if !n.attached() || n.arrayLength() < 2 {
		return
	}
	overlay := n.arrayOverlay()
	pieces := slices.Clone(overlay.piecesOf())
	slices.Reverse(pieces)
	for index := range pieces {
		p := &pieces[index]
		p.start = p.sourceIndex(p.length - 1)
		p.step = -p.step
	}
	overlay.replaceAllPieces(pieces)
	n.markStructural()
}

// tokens names every element in order by its source and source index.
func (overlay *arrayOverlay) tokens() []sortToken {
	tokens := make([]sortToken, 0, overlay.root.count())
	for _, p := range overlay.piecesOf() {
		for offset := range p.length {
			tokens = append(tokens, sortToken{p.source, p.sourceIndex(offset)})
		}
	}
	return tokens
}

// Reorder rearranges the elements so that the element now at position i is the one that was at permutation[i], the
// permutation of the "m" operation. It is the sort and reverse of tracker.ts with the resulting order given directly.
// permutation must name every current position exactly once.
func (array *Array) Reorder(permutation []int) error {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	if len(permutation) != n.arrayLength() {
		return errors.New("permutation length does not match the array length")
	}
	seen := make([]bool, len(permutation))
	identity := true
	for position, source := range permutation {
		if source < 0 || source >= len(seen) || seen[source] {
			return errors.New("permutation must name every array position exactly once")
		}
		seen[source] = true
		identity = identity && source == position
	}
	if !n.attached() || identity {
		return nil
	}
	overlay := n.arrayOverlay()
	current := overlay.tokens()
	order := make([]sortToken, len(permutation))
	for position, source := range permutation {
		order[position] = current[source]
	}
	overlay.replaceAllPieces(piecesFromOrder(order))
	n.markStructural()
	return nil
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
	if array.fillRangeEmpty(start, end) {
		return nil
	}
	stored, err := array.ref.placement(value)
	if err != nil {
		return err
	}
	n, mu := array.ref.lock()
	defer mu.Unlock()
	length := n.arrayLength()
	start, end = clampIndex(start, length), clampIndex(end, length)
	if end <= start || !n.attached() {
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

func (array *Array) fillRangeEmpty(start, end int) bool {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	length := n.arrayLength()
	return clampIndex(end, length) <= clampIndex(start, length)
}

// CopyWithin copies the elements in [start, end) over the elements from target, by value, as Array.prototype.copyWithin.
// A negative index counts from the end.
func (array *Array) CopyWithin(target, start, end int) error {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	if !n.attached() {
		return nil
	}
	length := n.arrayLength()
	target, start, end = clampIndex(target, length), clampIndex(start, length), clampIndex(end, length)
	count := min(max(end-start, 0), length-target)
	items := make([]any, count)
	for offset := range items {
		cloned, err := clonePlacementHeld(storedCurrent(n.valueAt(start+offset)), n.ctx)
		if err != nil {
			return err
		}
		items[offset] = cloned
	}
	n.splice(target, count, items)
	return nil
}

// overrideSnapshot is an element's override at the start of a sort.
type overrideSnapshot struct {
	set   bool
	value any
}

// sortElement is one element being sorted: its token, the value less receives, and its override at the start.
type sortElement struct {
	token    sortToken
	value    any
	snapshot overrideSnapshot
}

// entryOf is the existing entry of a token, or nil for a base element that has none.
func (n *node) entryOf(token sortToken) *entry {
	if token.source != nil {
		return token.source.entries[token.index]
	}
	return n.array.baseEntries.get(token.index)
}

// Sort stably orders the elements by less, as tracker.ts sort. less receives each element as a draft handle, or the
// value itself for a primitive, and may edit the draft, including the array: element writes made through the handles
// stay, while the result of the sort replaces the first min(length at start, length at end) slots and so overwrites
// slot writes and insertions the comparator made there. An element the comparator left in two slots becomes an
// independent clone in the later one.
func (array *Array) Sort(less func(a, b any) bool) {
	n, mu := array.ref.lock()
	if !n.attached() || n.arrayLength() < 2 {
		mu.Unlock()
		return
	}
	overlay := n.arrayOverlay()
	elements := make([]sortElement, 0, n.arrayLength())
	for _, p := range overlay.piecesOf() {
		for offset := range p.length {
			sourceIndex := p.sourceIndex(offset)
			element := sortElement{token: sortToken{p.source, sourceIndex}, value: n.slot(p, sourceIndex)}
			if isContainer(element.value) {
				element.value = n.entryValue(n.slotEntry(p, sourceIndex))
			}
			element.value = handle(element.value)
			if e := n.entryOf(element.token); e != nil {
				element.snapshot = overrideSnapshot{set: e.set, value: e.value}
			}
			elements = append(elements, element)
		}
	}
	generation := overlay.generation
	mu.Unlock()

	sort.SliceStable(elements, func(i, j int) bool { return less(elements[i].value, elements[j].value) })

	n, mu = array.ref.lock()
	defer mu.Unlock()
	if !n.attached() {
		return
	}
	for _, element := range elements {
		if e := n.entryOf(element.token); e != nil {
			e.set, e.value = element.snapshot.set, element.snapshot.value
		}
	}
	comparatorWasStructural := overlay.generation != generation
	currentLength := n.arrayLength()
	samePrefix := currentLength >= len(elements)
	for index := 0; samePrefix && index < len(elements); index++ {
		samePrefix = overlay.sameTokenAt(index, elements[index].token)
	}
	if !samePrefix {
		order := make([]sortToken, len(elements))
		for index, element := range elements {
			order[index] = element.token
		}
		n.replacePieceRange(0, min(len(order), currentLength), piecesFromOrder(order))
	}
	if comparatorWasStructural {
		n.deduplicate()
	}
}

// Snapshot returns a detached copy of the present content, as Object.Snapshot.
func (array *Array) Snapshot() []any {
	n, mu := array.ref.lock()
	defer mu.Unlock()
	value, _ := copyTrusted(n.current()).([]any)
	return value
}
