package delta

import (
	"errors"
	"maps"
	"math"
	"slices"
	"sync"
)

// maxDeltaOperations is tracker.ts MAX_DELTA_OPERATIONS: a larger batch folds
// into one root replacement.
const maxDeltaOperations = 4_096

// ErrRevoked is the panic value for any use of a draft handle after its change
// settled, was aborted, or became stale (tracker.ts "Cannot use a settled
// overlay"). Using a settled handle is a contract violation, so it panics
// like upstream's TypeError instead of returning an error every read must check.
var ErrRevoked = errors.New("Cannot use a settled overlay")

// ErrSparseArray reports an array write that would leave a hole (tracker.ts
// "Overlay arrays cannot contain holes").
var ErrSparseArray = errors.New("Overlay arrays cannot contain holes")

// errNotArrayIndex reports a write at a negative index. Upstream's draft sees
// "-1" as a property name that is not an array index (tracker.ts "Only array
// indices and length can be written").
var errNotArrayIndex = errors.New("Only array indices and length can be written")

type status int

const (
	statusOpen status = iota
	statusPrepared
	statusConsumed
	statusAborted
	statusStale
)

// Tracker owns one sequence of immutable object revisions.
type Tracker struct {
	mu       sync.Mutex
	value    map[string]any
	revision int
	contexts map[*overlay]struct{}
}

// Track takes immutable ownership of an alias-free strict-JSON root in O(1).
// The caller must never mutate initial again.
func Track(initial map[string]any) *Tracker {
	return &Tracker{value: initial, contexts: map[*overlay]struct{}{}}
}

// Value returns the latest adopted revision.
func (tracker *Tracker) Value() map[string]any {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.value
}

// Revision returns the number of adoptions.
func (tracker *Tracker) Revision() int {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.revision
}

// BeginChange opens an overlay draft over the latest revision. The draft
// never modifies that revision.
func (tracker *Tracker) BeginChange() *Change {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	context := &overlay{tracker: tracker, baseRevision: tracker.revision, base: tracker.value}
	context.root = &node{ctx: context, base: tracker.value}
	tracker.contexts[context] = struct{}{}
	return &Change{ctx: context}
}

// PrepareReplace prepares a whole-root replacement. A deeply equal value
// keeps the current root with empty operations.
func (tracker *Tracker) PrepareReplace(value map[string]any) *Prepared {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	context := &overlay{tracker: tracker, baseRevision: tracker.revision, base: tracker.value, status: statusPrepared}
	tracker.contexts[context] = struct{}{}
	if equalTrustedJSON(tracker.value, value) {
		return &Prepared{ctx: context, base: tracker.value, value: tracker.value, ops: []Op{}}
	}
	return &Prepared{ctx: context, base: tracker.value, value: value, ops: []Op{{"r", value}}}
}

// Adopt swaps the root pointer to a prepared revision and stales every
// competing change.
func (tracker *Tracker) Adopt(prepared *Prepared) error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	context := prepared.ctx
	if context == nil || context.tracker != tracker {
		return errors.New("Prepared change belongs to a different tracker")
	}
	context.mu.Lock()
	defer context.mu.Unlock()
	switch context.status {
	case statusConsumed:
		return errors.New("Prepared change has already been used")
	case statusAborted:
		return errors.New("Prepared change has been aborted")
	case statusStale:
		return errors.New("Prepared change is stale")
	case statusPrepared:
	default:
		return errors.New("Prepared change is not ready")
	}
	if context.baseRevision != tracker.revision || !sameContainer(tracker.value, prepared.base) {
		context.status = statusStale
		return errors.New("Prepared change is stale")
	}
	tracker.value = prepared.value
	context.status = statusConsumed
	tracker.revision++
	for other := range tracker.contexts {
		if other == context {
			continue
		}
		other.mu.Lock()
		if other.status == statusOpen || other.status == statusPrepared {
			other.status = statusStale
		}
		other.root = nil
		other.mu.Unlock()
	}
	clear(tracker.contexts)
	return nil
}

// Change is one open overlay draft.
type Change struct {
	ctx      *overlay
	prepared *Prepared
	settled  bool
}

// State returns the mutable root draft. It panics with ErrRevoked once the
// change settled.
func (change *Change) State() *Object {
	context := change.ctx
	if context == nil {
		panic(ErrRevoked)
	}
	context.mu.Lock()
	defer context.mu.Unlock()
	if context.root == nil || context.status != statusOpen {
		panic(ErrRevoked)
	}
	return &Object{n: context.root}
}

// Prepare materializes the candidate revision and its operations without
// changing authority. Every draft handle is unusable afterwards.
func (change *Change) Prepare() (*Prepared, error) {
	if change.settled {
		return nil, errors.New("Change has already been settled")
	}
	context := change.ctx
	change.ctx = nil
	change.settled = true
	context.mu.Lock()
	defer context.mu.Unlock()
	if context.status != statusOpen || context.root == nil {
		context.status = statusAborted
		return nil, ErrRevoked
	}
	context.status = statusPrepared
	root := context.root
	context.root = nil
	ops, err := emitOperations(root)
	if err != nil {
		context.status = statusAborted
		return nil, err
	}
	value := context.base
	if len(ops) > 0 {
		applied, err := ApplyImmutable(context.base, ops)
		if err != nil {
			context.status = statusAborted
			return nil, err
		}
		value, _ = applied.(map[string]any)
	}
	change.prepared = &Prepared{ctx: context, base: context.base, value: value, ops: ops}
	return change.prepared, nil
}

// Abort discards the change, or the preparation it produced.
func (change *Change) Abort() {
	if change.settled {
		if change.prepared != nil {
			change.prepared.Abort()
		}
		change.prepared = nil
		return
	}
	change.settled = true
	context := change.ctx
	change.ctx = nil
	context.mu.Lock()
	defer context.mu.Unlock()
	if context.status == statusOpen {
		context.status = statusAborted
	}
	context.root = nil
}

// Prepared is a materialized candidate revision.
type Prepared struct {
	ctx   *overlay
	base  map[string]any
	value map[string]any
	ops   []Op
}

// Base returns the revision the change started from.
func (prepared *Prepared) Base() map[string]any { return prepared.base }

// Value returns the candidate revision. Empty Ops means Value is Base.
func (prepared *Prepared) Value() map[string]any { return prepared.value }

// Ops returns the exact operations that transform Base into Value. Payloads
// may be the same containers as parts of Value.
func (prepared *Prepared) Ops() []Op { return prepared.ops }

// BaseRevision returns the tracker revision the change started from.
func (prepared *Prepared) BaseRevision() int { return prepared.ctx.baseRevision }

// Abort prevents adoption. The candidate stays readable.
func (prepared *Prepared) Abort() {
	context := prepared.ctx
	context.mu.Lock()
	defer context.mu.Unlock()
	if context.status == statusPrepared {
		context.status = statusAborted
	}
}

// overlay is tracker.ts OverlayContext: one change's draft state.
type overlay struct {
	mu           sync.Mutex
	tracker      *Tracker
	baseRevision int
	status       status
	base         map[string]any
	root         *node
}

func (context *overlay) assertOpen() {
	if context.status != statusOpen || context.root == nil {
		panic(ErrRevoked)
	}
}

// node is one container reached through the draft. Its base is either a
// container of the base revision or a detached placement clone; the overlay
// records changes without touching the base.
type node struct {
	ctx    *overlay
	parent *node
	key    string
	entry  *entry
	base   any

	// Object overlay.
	writes  map[string]any
	order   []string       // written keys in write order; a rewritten key appears again, only its last position is live
	orderAt map[string]int // the live position in order of each written key
	deletes map[string]bool
	readded map[string]bool // base keys deleted and then written again
	kids    map[string]*node

	// Array overlay; nil until the array is first accessed.
	entries    []*entry
	structural bool

	changed bool
}

// entry is one element of an array overlay.
type entry struct {
	index int // base index, or -1 for an inserted element
	value any // primitive or *node, when set
	set   bool
	kid   *node
	live  bool
}

func (n *node) isArray() bool {
	_, ok := n.base.([]any)
	return ok
}

// attached reports whether n is still reachable from the root at the slot
// it was read from. Writes through a detached handle are ignored.
func (n *node) attached() bool {
	parent := n.parent
	if parent == nil {
		return n == n.ctx.root
	}
	if !parent.attached() {
		return false
	}
	if n.entry != nil {
		e := n.entry
		if !e.live {
			return false
		}
		if e.set {
			return e.value == n
		}
		return e.kid == n
	}
	if written, ok := parent.writes[n.key]; ok {
		return written == n
	}
	return !parent.deletes[n.key] && parent.kids[n.key] == n
}

func (n *node) markChanged() { n.changed = true }

// modified reports whether n or an attached descendant changed.
func (n *node) modified() bool {
	if n.changed {
		return true
	}
	for key, kid := range n.kids {
		if _, written := n.writes[key]; written || n.deletes[key] {
			continue
		}
		if kid.modified() {
			return true
		}
	}
	for _, e := range n.entries {
		if !e.set && e.kid != nil && e.kid.modified() {
			return true
		}
	}
	return false
}

// wrap stores a value as an overlay slot: containers become placement nodes.
func (n *node) wrap(value any, key string, e *entry) any {
	if !isContainer(value) {
		return value
	}
	return &node{ctx: n.ctx, parent: n, key: key, entry: e, base: value}
}

func (n *node) ensureEntries() {
	if n.entries != nil {
		return
	}
	base := n.base.([]any)
	n.entries = make([]*entry, len(base))
	for index := range base {
		n.entries[index] = &entry{index: index, live: true}
	}
}

func (n *node) objectLookup(key string) (any, bool) {
	if written, ok := n.writes[key]; ok {
		return written, true
	}
	if n.deletes[key] {
		return nil, false
	}
	value, ok := n.base.(map[string]any)[key]
	if !ok {
		return nil, false
	}
	if !isContainer(value) {
		return value, true
	}
	kid := n.kids[key]
	if kid == nil {
		if n.kids == nil {
			n.kids = map[string]*node{}
		}
		kid = &node{ctx: n.ctx, parent: n, key: key, base: value}
		n.kids[key] = kid
	}
	return kid, true
}

func (n *node) objectSet(key string, stored any) {
	if n.writes == nil {
		n.writes = map[string]any{}
	}
	if _, seen := n.writes[key]; !seen {
		if n.orderAt == nil {
			n.orderAt = map[string]int{}
		}
		n.orderAt[key] = len(n.order)
		n.order = append(n.order, key)
	}
	n.writes[key] = n.wrap(stored, key, nil)
	if n.deletes[key] {
		if n.readded == nil {
			n.readded = map[string]bool{}
		}
		n.readded[key] = true
	}
	delete(n.deletes, key)
	n.markChanged()
}

// writtenKeys returns the keys written and not deleted since, in write order.
func (n *node) writtenKeys() []string {
	keys := make([]string, 0, len(n.writes))
	for position, key := range n.order {
		if _, written := n.writes[key]; written && n.orderAt[key] == position {
			keys = append(keys, key)
		}
	}
	return keys
}

func (n *node) objectDelete(key string) {
	delete(n.writes, key)
	if _, ok := n.base.(map[string]any)[key]; ok {
		if n.deletes == nil {
			n.deletes = map[string]bool{}
		}
		n.deletes[key] = true
	}
	n.markChanged()
}

func (n *node) entryValue(e *entry) any {
	if e.set {
		return e.value
	}
	value := n.base.([]any)[e.index]
	if !isContainer(value) {
		return value
	}
	if e.kid == nil {
		e.kid = &node{ctx: n.ctx, parent: n, entry: e, base: value}
	}
	return e.kid
}

func (n *node) newEntry(stored any) *entry {
	e := &entry{index: -1, set: true, live: true}
	e.value = n.wrap(stored, "", e)
	return e
}

// current materializes the node's present content, sharing unchanged
// containers with its base.
func (n *node) current() any {
	if !n.modified() {
		return n.base
	}
	if !n.isArray() {
		base := n.base.(map[string]any)
		hint := 0
		if len(base) < math.MaxInt32 && len(n.writes) < math.MaxInt32 {
			hint = len(base) + len(n.writes)
		}
		result := make(map[string]any, hint)
		for key, value := range base {
			if n.deletes[key] {
				continue
			}
			if kid := n.kids[key]; kid != nil {
				result[key] = kid.current()
				continue
			}
			result[key] = value
		}
		for key, stored := range n.writes {
			result[key] = storedCurrent(stored)
		}
		return result
	}
	if n.entries == nil {
		return n.base
	}
	result := make([]any, len(n.entries))
	for index, e := range n.entries {
		result[index] = storedCurrent(n.entryValue(e))
	}
	return result
}

func storedCurrent(stored any) any {
	if kid, ok := stored.(*node); ok {
		return kid.current()
	}
	return stored
}

// emitOperations computes the exact batch for a draft, folding a batch
// larger than maxDeltaOperations into one root replacement.
func emitOperations(root *node) ([]Op, error) {
	ops := []Op{}
	if !root.modified() {
		return ops, nil
	}
	emitNode(root, []any{}, &ops)
	if len(ops) > maxDeltaOperations {
		value, err := clonePlacement(root.current())
		if err != nil {
			return nil, err
		}
		return []Op{{"r", value}}, nil
	}
	return ops, nil
}

func childPath(path []any, segment any) []any {
	next := make([]any, len(path)+1)
	copy(next, path)
	next[len(path)] = segment
	return next
}

func emitNode(n *node, path []any, ops *[]Op) {
	if len(*ops) > maxDeltaOperations {
		return
	}
	if !n.isArray() {
		emitObject(n, path, ops)
		return
	}
	emitArray(n, path, ops)
}

func emitObject(n *node, path []any, ops *[]Op) {
	if hasReservedMutation(n) {
		emitFold(n, path, ops)
		return
	}
	base := n.base.(map[string]any)
	for _, key := range n.writtenKeys() {
		stored := n.writes[key]
		before, present := base[key]
		emitChangedValue(ops, childPath(path, key), before, present, storedCurrent(stored))
	}
	for _, key := range slices.Sorted(maps.Keys(n.deletes)) {
		*ops = append(*ops, Op{"d", childPath(path, key)})
	}
	for _, key := range slices.Sorted(maps.Keys(n.kids)) {
		if _, written := n.writes[key]; written || n.deletes[key] {
			continue
		}
		if kid := n.kids[key]; kid.modified() {
			emitNode(kid, childPath(path, key), ops)
		}
	}
}

// hasReservedMutation reports whether a path through this object node would name a reserved segment: a write or
// delete of a reserved key (tracker.ts:1814 hasReservedMutation), or an edit below a reserved key, which upstream folds
// at path.slice(0, reservedAt) (tracker.ts:1638-1643). Emission walks top-down, so the first reserved segment folds.
func hasReservedMutation(n *node) bool {
	for key := range n.writes {
		if reservedSegments[key] {
			return true
		}
	}
	for key := range n.deletes {
		if reservedSegments[key] {
			return true
		}
	}
	for key, kid := range n.kids {
		if reservedSegments[key] && kid.modified() {
			return true
		}
	}
	return false
}

// emitFold sets the node's whole current value at path, or replaces the root (tracker.ts:1659 and emitSet :2031).
func emitFold(n *node, path []any, ops *[]Op) {
	value, err := CloneJSON(n.current())
	if err != nil {
		// Tracked content is strict JSON by construction (placements are strict-cloned).
		panic(err)
	}
	if len(path) == 0 {
		*ops = append(*ops, Op{"r", value})
		return
	}
	*ops = append(*ops, Op{"s", path, value})
}

// emitArray emits removals of base elements (from the end), a permutation of
// retained base elements, insertion runs, and then element changes at final
// indices, mirroring tracker.ts emitArrayOperations and buildArrayPlan.
func emitArray(n *node, path []any, ops *[]Op) {
	if n.entries == nil {
		return
	}
	base := n.base.([]any)
	regions := denseRegions(n)
	for _, region := range regions {
		items := make([]any, region.length)
		for offset := range items {
			cloned, err := CloneJSON(storedCurrent(n.entryValue(n.entries[region.start+offset])))
			if err != nil {
				// Tracked content is strict JSON by construction (placements are strict-cloned).
				panic(err)
			}
			items[offset] = cloned
		}
		*ops = append(*ops, Op{"p", slices.Clone(path), region.start, region.length, items})
	}
	if n.structural {
		retained := make([]bool, len(base))
		var targetBase []int
		for _, e := range n.entries {
			if e.index >= 0 {
				retained[e.index] = true
				targetBase = append(targetBase, e.index)
			}
		}
		for end := len(base); end > 0; {
			if retained[end-1] {
				end--
				continue
			}
			start := end - 1
			for start > 0 && !retained[start-1] {
				start--
			}
			*ops = append(*ops, Op{"p", slices.Clone(path), start, end - start, []any{}})
			end = start
		}
		retainedBase := make([]int, 0, len(targetBase))
		for index, kept := range retained {
			if kept {
				retainedBase = append(retainedBase, index)
			}
		}
		if !slices.Equal(targetBase, retainedBase) {
			positions := make(map[int]int, len(retainedBase))
			for position, index := range retainedBase {
				positions[index] = position
			}
			permutation := make([]any, len(targetBase))
			for position, index := range targetBase {
				permutation[position] = positions[index]
			}
			*ops = append(*ops, Op{"m", slices.Clone(path), permutation})
		}
		for index := 0; index < len(n.entries); {
			if n.entries[index].index >= 0 {
				index++
				continue
			}
			start := index
			var items []any
			for index < len(n.entries) && n.entries[index].index < 0 {
				items = append(items, storedCurrent(n.entries[index].value))
				index++
			}
			*ops = append(*ops, Op{"p", slices.Clone(path), start, 0, items})
		}
	}
	for position, e := range n.entries {
		if len(*ops) > maxDeltaOperations {
			return
		}
		if e.index < 0 || inDenseRegion(regions, position) {
			continue
		}
		if e.set {
			emitChangedValue(ops, childPath(path, position), base[e.index], true, storedCurrent(e.value))
			continue
		}
		if e.kid != nil && e.kid.modified() {
			emitNode(e.kid, childPath(path, position), ops)
		}
	}
}

// A dense region is a run of at least denseRegionMinimum edited elements of an array, with at most one unedited element
// between neighbors and at least half its positions edited, that one splice replaces instead of one operation per edit
// (tracker.ts buildDenseRegions).
const denseRegionMinimum = 256

type denseRegion struct{ start, length int }

func inDenseRegion(regions []denseRegion, position int) bool {
	for _, region := range regions {
		if position >= region.start && position < region.start+region.length {
			return true
		}
	}
	return false
}

// denseRegions finds the regions of a non-structural array, counting a position when its element was replaced or when
// the nearest array above an edited descendant is this array (tracker.ts recordDenseArrayPosition).
func denseRegions(n *node) []denseRegion {
	if n.structural || len(n.entries) < denseRegionMinimum {
		return nil
	}
	edited := make([]bool, len(n.entries))
	total := 0
	for position, e := range n.entries {
		if e.set || (e.kid != nil && editsNearestArray(e.kid)) {
			edited[position] = true
			total++
		}
	}
	if total < denseRegionMinimum {
		return nil
	}
	var regions []denseRegion
	for at := 0; at < len(edited); {
		for at < len(edited) && !edited[at] {
			at++
		}
		if at == len(edited) {
			break
		}
		start, end, count, gap := at, at, 0, 0
		for at < len(edited) {
			if edited[at] {
				count++
				end = at
				gap = 0
			} else if gap++; gap > 1 {
				break
			}
			at++
		}
		if length := end - start + 1; count >= denseRegionMinimum && count*2 >= length {
			regions = append(regions, denseRegion{start, length})
		}
	}
	return regions
}

// editsNearestArray reports whether n, or an object below it with no array between, was edited itself.
func editsNearestArray(n *node) bool {
	if n.changed {
		return true
	}
	if n.isArray() {
		return false
	}
	for key, kid := range n.kids {
		if _, written := n.writes[key]; written || n.deletes[key] {
			continue
		}
		if editsNearestArray(kid) {
			return true
		}
	}
	return false
}

// emitChangedValue is tracker.ts emitChangedValue: equal values emit nothing,
// a string extension emits an append, and anything else a set.
func emitChangedValue(ops *[]Op, path []any, before any, present bool, after any) {
	if present {
		if !isContainer(after) && !isContainer(before) && equalTrustedJSON(before, after) {
			return
		}
		if isContainer(before) && isContainer(after) && equalTrustedJSON(before, after) {
			return
		}
		previous, wasString := before.(string)
		next, isString := after.(string)
		if wasString && isString {
			if len(next) > len(previous) && next[:len(previous)] == previous {
				*ops = append(*ops, Op{"a", path, next[len(previous):]})
				return
			}
			if shared := Overlap(previous, next, 65_536); shared > 0 {
				*ops = append(*ops, Op{"t", path, utf16Len(previous) - shared})
				if rest := utf16Suffix(next, shared); rest != "" {
					*ops = append(*ops, Op{"a", slices.Clone(path), rest})
				}
				return
			}
		}
	}
	*ops = append(*ops, Op{"s", path, after})
}
