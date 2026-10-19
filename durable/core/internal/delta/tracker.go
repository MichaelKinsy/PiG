package delta

import (
	"errors"
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// maxDeltaOperations is tracker.ts MAX_DELTA_OPERATIONS: a larger batch folds
// into one root replacement.
const maxDeltaOperations = 4_096

// ErrRevoked is the panic value for any use of a draft handle after its change settled, was aborted, or became stale
// (tracker.ts "Cannot use a settled overlay"). Using a settled handle is a contract violation, so it panics like
// upstream's TypeError instead of returning an error every read must check.
// Read-only: assigned once at initialization.
var ErrRevoked = errors.New("Cannot use a settled overlay")

type status int

const (
	statusOpen status = iota
	statusPrepared
	statusConsumed
	statusAborted
	statusStale
)

// Tracker owns one sequence of immutable revisions of an object or array root, as tracker.ts's track(initial: T extends object).
type Tracker struct {
	value    any // *jv.Object or []any
	revision int
}

// Track takes immutable ownership of an alias-free JSON root in O(1). The caller must never mutate initial again.
func Track(initial *jv.Object) *Tracker { return &Tracker{value: initial} }

// TrackArray is Track for an array root. Its Change drafts through Elements, and its operations address the root with the empty path.
func TrackArray(initial []any) *Tracker { return &Tracker{value: initial} }

// Value returns the latest adopted revision of an object root, and nil for an array root; Root returns either.
func (tracker *Tracker) Value() *jv.Object {
	value, _ := tracker.value.(*jv.Object)
	return value
}

// Root returns the latest adopted revision: a *jv.Object or a []any.
func (tracker *Tracker) Root() any { return tracker.value }

// Revision returns the number of adoptions.
func (tracker *Tracker) Revision() int { return tracker.revision }

// BeginChange opens an overlay draft over the latest revision. The draft never modifies that revision.
func (tracker *Tracker) BeginChange() *Change {
	context := &overlay{tracker: tracker, baseRevision: tracker.revision, base: tracker.value}
	context.root = &node{ctx: context, base: tracker.value}
	return &Change{ctx: context}
}

// Adopt makes a prepared revision the tracker's latest and stales every competing change.
func (tracker *Tracker) Adopt(prepared *Prepared) error {
	context := prepared.ctx
	if context == nil || context.tracker != tracker {
		return errors.New("Prepared change belongs to a different tracker")
	}
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
	if context.baseRevision != tracker.revision {
		context.status = statusStale
		return errors.New("Prepared change is stale")
	}
	tracker.value = prepared.value
	context.status = statusConsumed
	tracker.revision++
	return nil
}

// Change is one open overlay draft.
type Change struct {
	ctx      *overlay
	prepared *Prepared
	settled  bool
}

// State returns the mutable draft of an object root, and nil for an array root. It panics with ErrRevoked once the
// change settled.
func (change *Change) State() *Obj {
	root := change.root()
	if root.isArray() {
		return nil
	}
	return &Obj{n: root}
}

// Elements returns the mutable draft of an array root, and nil for an object root. It panics with ErrRevoked once the
// change settled.
func (change *Change) Elements() *Arr {
	root := change.root()
	if !root.isArray() {
		return nil
	}
	return &Arr{n: root}
}

func (change *Change) root() *node {
	context := change.ctx
	if context == nil {
		panic(ErrRevoked)
	}
	context.assertOpen()
	return context.root
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
	if context.status != statusOpen || context.root == nil || context.baseRevision != context.tracker.revision {
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
		applied, err := Apply(context.base, ops)
		if err != nil {
			context.status = statusAborted
			return nil, err
		}
		value = applied
	}
	change.prepared = &Prepared{ctx: context, base: context.base, value: value, ops: ops}
	return change.prepared, nil
}

// PrepareThen is Prepare for a draft the core edited and a host's Chord tracker then continued: ops were emitted against
// the draft's present content, so they follow the draft's own operations. The candidate's operations are both lists
// in order, and its value the base with them applied.
func (change *Change) PrepareThen(ops []Op) (*Prepared, error) {
	prepared, err := change.Prepare()
	if err != nil || len(ops) == 0 {
		return prepared, err
	}
	applied, err := Apply(prepared.value, ops)
	if err != nil {
		prepared.Abort()
		return nil, err
	}
	prepared.value = applied
	prepared.ops = append(append(make([]Op, 0, len(prepared.ops)+len(ops)), prepared.ops...), ops...)
	return prepared, nil
}

// BaseRoot returns the revision an open change started from: a *jv.Object or a []any. It is nil once the change settled.
func (change *Change) BaseRoot() any {
	if change.ctx == nil {
		return nil
	}
	return change.ctx.base
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
	if context.status == statusOpen {
		context.status = statusAborted
	}
	context.root = nil
}

// Prepared is a materialized candidate revision.
type Prepared struct {
	ctx   *overlay
	base  any
	value any
	ops   []Op
}

// Base returns the revision the change started from, for an object root (nil for an array root; see BaseRoot).
func (prepared *Prepared) Base() *jv.Object {
	base, _ := prepared.base.(*jv.Object)
	return base
}

// Value returns the candidate revision of an object root (nil for an array root; see ValueRoot). Empty Ops means Value is Base.
func (prepared *Prepared) Value() *jv.Object {
	value, _ := prepared.value.(*jv.Object)
	return value
}

// BaseRoot returns the revision the change started from: a *jv.Object or a []any.
func (prepared *Prepared) BaseRoot() any { return prepared.base }

// ValueRoot returns the candidate revision: a *jv.Object or a []any. Empty Ops means ValueRoot is BaseRoot.
func (prepared *Prepared) ValueRoot() any { return prepared.value }

// Ops returns the exact operations that transform Base into Value. Payloads
// may be the same containers as parts of Value.
func (prepared *Prepared) Ops() []Op { return prepared.ops }

// BaseRevision returns the tracker revision the change started from.
func (prepared *Prepared) BaseRevision() int { return prepared.ctx.baseRevision }

// Abort prevents adoption. The candidate stays readable.
func (prepared *Prepared) Abort() {
	context := prepared.ctx
	if context.status == statusPrepared {
		context.status = statusAborted
	}
}

// overlay is tracker.ts OverlayContext: one change's draft state.
type overlay struct {
	tracker      *Tracker
	baseRevision int
	status       status
	base         any
	root         *node
	dirtySeq     int // nodes that have changed so far
	writeSeq     int // base elements overwritten so far
}

func (context *overlay) assertOpen() {
	if context.status != statusOpen || context.root == nil || context.baseRevision != context.tracker.revision {
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
	writes      map[string]any
	order       []string       // written keys in write order; a rewritten key appears again, only its last position is live
	orderAt     map[string]int // the live position in order of each written key
	deletes     map[string]bool
	deleteOrder []string        // deleted base keys in deletion order (tracker.ts deletes Set)
	readded     map[string]bool // base keys deleted and then written again
	kids        map[string]*node

	// Array overlay; nil until the array is first accessed.
	entries    []*entry
	structural bool

	changed bool
	seq     int // 1-based order in which the node first changed (tracker.ts context.dirty)
}

// entry is one element of an array overlay.
type entry struct {
	index  int // base index, or -1 for an inserted element
	value  any // primitive or *node, when set
	set    bool
	setSeq int // order in which a base element was first overwritten (tracker.ts baseOverrides insertion order)
	kid    *node
	live   bool
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

func (n *node) markChanged() {
	if !n.changed {
		n.changed = true
		n.ctx.dirtySeq++
		n.seq = n.ctx.dirtySeq
	}
}

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
	value, ok := n.base.(*jv.Object).Get(key)
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
	// Writing the value a key already holds changes nothing and does not dirty the node (tracker.ts setProperty).
	if current, present := n.objectLookup(key); present && !n.deletes[key] && !isContainer(stored) && !isContainer(current) && current == stored {
		return
	}
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
	if n.deletes[key] && n.base.(*jv.Object).Has(key) {
		if n.readded == nil {
			n.readded = map[string]bool{}
		}
		n.readded[key] = true
	}
	if n.deletes[key] {
		n.deleteOrder = slices.DeleteFunc(n.deleteOrder, func(other string) bool { return other == key })
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
	if _, present := n.objectLookup(key); !present {
		return
	}
	delete(n.writes, key)
	delete(n.readded, key)
	// tracker.ts records the deletion of a key the base lacks too (a key written and then deleted); emission skips it, but
	// a reserved key still makes the node fold.
	if n.deletes == nil {
		n.deletes = map[string]bool{}
	}
	if !n.deletes[key] {
		n.deleteOrder = append(n.deleteOrder, key)
	}
	n.deletes[key] = true
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
		base := n.base.(*jv.Object)
		result := jv.NewObjectCap(base.Len() + len(n.writes))
		base.Members(func(key string, value any) bool {
			if n.deletes[key] || n.readded[key] {
				return true
			}
			if written, ok := n.writes[key]; ok {
				result.Set(key, storedCurrent(written))
			} else if kid := n.kids[key]; kid != nil {
				result.Set(key, kid.current())
			} else {
				result.Set(key, value)
			}
			return true
		})
		for _, key := range n.writtenKeys() {
			if base.Has(key) && !n.readded[key] {
				continue
			}
			result.Set(key, storedCurrent(n.writes[key]))
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
//
// Out of line: TinyGo would inline this single-caller function into Prepare, and V8 compiles a function that runs hot as one unit, at a cost that grows with its body (host/cf/test/functionsize.test.ts).
//
//go:noinline
func emitOperations(root *node) ([]Op, error) {
	ops := []Op{}
	if !root.modified() {
		return ops, nil
	}
	var emissions []emission
	collectEmissions(root, Path{}, root.ctx.dirtySeq, &emissions)
	// tracker.ts emitOperations buckets nodes by path length, keeping the order in which they first became dirty within a bucket.
	slices.SortStableFunc(emissions, func(a, b emission) int {
		if len(a.path) != len(b.path) {
			return len(a.path) - len(b.path)
		}
		return a.seq - b.seq
	})
	for _, e := range emissions {
		if len(ops) > maxDeltaOperations {
			break
		}
		if e.n.isArray() {
			emitArray(e.n, e.path, &ops)
		} else {
			emitObject(e.n, e.path, &ops)
		}
	}
	if len(ops) > maxDeltaOperations {
		return []Op{{Verb: Replace, Value: jv.Clone(root.current())}}, nil
	}
	return ops, nil
}

// emission is one node whose own changes become operations at path.
type emission struct {
	n    *node
	path Path
	seq  int
}

// collectEmissions lists the nodes that emit operations, without descending below a node whose whole value is folded into one set. A folded node that was not itself dirty takes the next position after the dirty ones, as a forced fold is appended to tracker.ts's emission map.
func collectEmissions(n *node, path Path, fold int, out *[]emission) {
	if !n.isArray() && hasReservedMutation(n) {
		seq := n.seq
		if !n.changed {
			seq = fold + len(*out) + 1
		}
		*out = append(*out, emission{n, path, seq})
		return
	}
	if n.changed {
		*out = append(*out, emission{n, path, n.seq})
	}
	if n.isArray() {
		regions := denseRegions(n)
		if len(regions) > 0 && !n.changed {
			*out = append(*out, emission{n, path, fold + len(*out) + 1})
		}
		for position, e := range n.entries {
			if e.index < 0 || e.set || inDenseRegion(regions, position) {
				continue
			}
			if e.kid != nil && e.kid.modified() {
				collectEmissions(e.kid, childIdx(path, position), fold, out)
			}
		}
		return
	}
	for _, key := range sortedKids(n.kids) {
		if _, written := n.writes[key]; written || n.deletes[key] {
			continue
		}
		if kid := n.kids[key]; kid.modified() {
			collectEmissions(kid, childKey(path, key), fold, out)
		}
	}
}

func childKey(path Path, key string) Path {
	next := make(Path, len(path)+1)
	copy(next, path)
	next[len(path)] = Key(key)
	return next
}

func childIdx(path Path, index int) Path {
	next := make(Path, len(path)+1)
	copy(next, path)
	next[len(path)] = Idx(index)
	return next
}

// Out of line: TinyGo would inline this single-caller function into emitOperations, and V8 compiles a function that runs hot as one unit, at a cost that grows with its body (host/cf/test/functionsize.test.ts).
//
//go:noinline
func emitObject(n *node, path Path, ops *[]Op) {
	if hasReservedMutation(n) {
		emitFold(n, path, ops)
		return
	}
	base := n.base.(*jv.Object)
	// Replay must encode delete-and-re-add explicitly so string-key order matches the draft (tracker.ts emitObjectOperations).
	for _, key := range n.writtenKeys() {
		if inBase := base.Has(key); inBase && n.readded[key] {
			*ops = append(*ops, Op{Verb: Delete, Path: childKey(path, key)})
		}
	}
	for _, key := range n.writtenKeys() {
		stored := n.writes[key]
		before, present := base.Get(key)
		if n.readded[key] {
			present = false
		}
		emitChangedValue(ops, childKey(path, key), before, present, storedCurrent(stored))
	}
	for _, key := range n.deleteOrder {
		if base.Has(key) {
			*ops = append(*ops, Op{Verb: Delete, Path: childKey(path, key)})
		}
	}
}

// hasReservedMutation reports whether a path through this object node would name a reserved segment: a write or
// delete of a reserved key (tracker.ts:1814 hasReservedMutation), or an edit below a reserved key, which upstream folds
// at path.slice(0, reservedAt) (tracker.ts:1638-1643). Emission walks top-down, so the first reserved segment folds.
func hasReservedMutation(n *node) bool {
	for key := range n.writes {
		if isReserved(key) {
			return true
		}
	}
	for key := range n.deletes {
		if isReserved(key) {
			return true
		}
	}
	for key, kid := range n.kids {
		if isReserved(key) && kid.modified() {
			return true
		}
	}
	return false
}

// emitFold sets the node's whole current value at path, or replaces the root (tracker.ts:1659 and emitSet :2031).
func emitFold(n *node, path Path, ops *[]Op) {
	value := jv.Clone(n.current())
	if len(path) == 0 {
		*ops = append(*ops, Op{Verb: Replace, Value: value})
		return
	}
	*ops = append(*ops, Op{Verb: Set, Path: path, Value: value})
}

// emitArray emits removals of base elements (from the end), a permutation of
// retained base elements, insertion runs, and then element changes at final
// indices, mirroring tracker.ts emitArrayOperations and buildArrayPlan.
func emitArray(n *node, path Path, ops *[]Op) {
	if n.entries == nil {
		return
	}
	base := n.base.([]any)
	regions := denseRegions(n)
	for _, region := range regions {
		items := make([]any, region.length)
		for offset := range items {
			items[offset] = jv.Clone(storedCurrent(n.entryValue(n.entries[region.start+offset])))
		}
		*ops = append(*ops, Op{Verb: Splice, Path: slices.Clone(path), Index: region.start, Remove: region.length, Items: items})
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
			*ops = append(*ops, Op{Verb: Splice, Path: slices.Clone(path), Index: start, Remove: end - start, Items: []any{}})
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
			permutation := make([]int, len(targetBase))
			for position, index := range targetBase {
				permutation[position] = positions[index]
			}
			*ops = append(*ops, Op{Verb: Permute, Path: slices.Clone(path), Perm: permutation})
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
			*ops = append(*ops, Op{Verb: Splice, Path: slices.Clone(path), Index: start, Items: items})
		}
	}
	var overrides []int
	for position, e := range n.entries {
		if e.index >= 0 && e.set && !inDenseRegion(regions, position) {
			overrides = append(overrides, position)
		}
	}
	slices.SortStableFunc(overrides, func(a, b int) int { return n.entries[a].setSeq - n.entries[b].setSeq })
	for _, position := range overrides {
		if len(*ops) > maxDeltaOperations {
			return
		}
		e := n.entries[position]
		emitChangedValue(ops, childIdx(path, position), base[e.index], true, storedCurrent(e.value))
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
func emitChangedValue(ops *[]Op, path Path, before any, present bool, after any) {
	if present {
		if !isContainer(after) && !isContainer(before) && jv.Equal(before, after) {
			return
		}
		if isContainer(before) && isContainer(after) && jv.Equal(before, after) {
			return
		}
		previous, wasString := before.(string)
		next, isString := after.(string)
		if wasString && isString {
			if len(next) > len(previous) && next[:len(previous)] == previous {
				*ops = append(*ops, Op{Verb: Append, Path: path, Text: next[len(previous):]})
				return
			}
			if shared := Overlap(previous, next, 65_536); shared > 0 {
				*ops = append(*ops, Op{Verb: Trim, Path: path, Count: jv.UTF16Len(previous) - shared})
				if rest := jv.SliceFrom(next, shared); rest != "" {
					*ops = append(*ops, Op{Verb: Append, Path: slices.Clone(path), Text: rest})
				}
				return
			}
		}
	}
	*ops = append(*ops, Op{Verb: Set, Path: path, Value: after})
}

func isContainer(value any) bool {
	switch value.(type) {
	case *jv.Object, []any:
		return true
	}
	return false
}

// Overlap returns the longest suffix of a that is a prefix of b, in UTF-16
// code units, considering at most the last scan units of a. It probes a long
// head of b first, then one unit, and gives up after eight candidates per
// probe; giving up returns 0, which emits a set: larger, never wrong.
func Overlap(a, b string, scan int) int {
	if a == "" || b == "" || scan == 0 {
		return 0
	}
	left := jv.UTF16Units(a)
	right := jv.UTF16Units(b)
	tail := left
	if len(tail) > scan {
		tail = tail[len(tail)-scan:]
	}
	const probe, maxCandidates = 64, 8
	for _, h := range []int{min(probe, len(right)), 1} {
		head := right[:h]
		tried := 0
		for k := indexUnits(tail, head, 0); k != -1; k = indexUnits(tail, head, k+1) {
			tried++
			if tried > maxCandidates {
				break
			}
			n := len(tail) - k
			if n <= len(right) && slices.Equal(tail[k:], right[:n]) {
				return n
			}
		}
		if h == 1 {
			break
		}
	}
	return 0
}

func indexUnits(haystack, needle []uint16, from int) int {
	for index := from; index+len(needle) <= len(haystack); index++ {
		if slices.Equal(haystack[index:index+len(needle)], needle) {
			return index
		}
	}
	return -1
}

func sortedKids(kids map[string]*node) []string {
	keys := make([]string, 0, len(kids))
	for k := range kids {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
