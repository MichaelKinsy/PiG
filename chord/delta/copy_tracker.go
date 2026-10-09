package delta

import (
	"fmt"
	"math"
	"reflect"
	"weak"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// copyTracker is the working-copy form of tracker.ts behind DiffCandidate: Change.State returns a private deep copy of the base revision that the caller edits with ordinary map and slice operations, and Prepare validates and de-aliases the edited copy, restores the identity of every container that ended up equal to its base counterpart, and diffs the base against the result. Tracker (tracker.go) is the overlay port of tracker.ts that drafts use.

// copyTracker owns the revision sequence of one strict-JSON root. One tracker is one revision sequence; delivery order and persistence belong to the surrounding protocol.
type copyTracker struct {
	owner    *byte
	value    JsonValue
	revision int
	live     []weak.Pointer[copyState]
}

type copyStatus uint8

const (
	copyStatusOpen copyStatus = iota
	copyStatusPrepared
	copyStatusConsumed
	copyStatusAborted
	copyStatusStale
)

// copyState is the shared state of one Change and the Prepared it produced. It holds no revision data after it settles, so a retained settled Change or Prepared never keeps the base alive through it.
type copyState struct {
	status       copyStatus
	baseRevision int
	base         JsonValue
	working      JsonValue
	origin       map[identity]any
	edited       bool // the working copy exists
}

// copyPrepared is one fully materialized candidate revision. Base, Value and Ops, including op paths and payloads, are immutable by contract, and payloads may be the same containers as parts of Value. An empty Ops means Value is Base.
type copyPrepared struct {
	Base         JsonValue
	Value        JsonValue
	Ops          []Op
	BaseRevision int

	owner *byte
	state *copyState
}

// Abort prevents adoption. The candidate stays readable. Abort is idempotent.
func (p *copyPrepared) Abort() { abort(p.state) }

// copyChange is one open transaction over the tracker's latest revision.
type copyChange struct {
	tracker *copyTracker
	state   *copyState
	settled bool
}

// trackCopy takes immutable ownership of an alias-free strict-JSON root in O(1). The root must not be mutated again.
func trackCopy(initial JsonValue) *copyTracker {
	return &copyTracker{owner: new(byte), value: initial}
}

// Value is the latest adopted revision.
func (t *copyTracker) Value() JsonValue { return t.value }

// BeginChange opens a transaction over the latest revision without modifying it. The transaction may stay open across blocking calls; it ends with Prepare or Abort.
func (t *copyTracker) BeginChange() *copyChange {
	state := &copyState{status: copyStatusOpen, baseRevision: t.revision, base: t.value}
	t.register(state)
	return &copyChange{tracker: t, state: state}
}

// PrepareReplace is a whole-root operation, not a diff. It takes immutable ownership of value in O(1) and never walks it, except to compare it with the current root. When deeply equal to the current root the batch is empty and the current root is kept, otherwise it is [["r", value]].
func (t *copyTracker) PrepareReplace(value JsonValue) *copyPrepared {
	state := &copyState{status: copyStatusPrepared, baseRevision: t.revision}
	t.register(state)
	prepared := &copyPrepared{Base: t.value, Ops: []Op{}, BaseRevision: t.revision, owner: t.owner, state: state}
	if equalJson(t.value, value) {
		prepared.Value = t.value
		return prepared
	}
	prepared.Value = value
	prepared.Ops = []Op{{"r", value}}
	return prepared
}

// PrepareCandidate prepares a revision from a complete candidate the caller built, for callers that edit a decoded copy of the value (typed state) instead of the working copy of a Change. The candidate is validated, restored to the base's container identity where it is deeply equal, and diffed against the base; the tracker takes ownership of it. An invalid candidate fails without preparing anything.
func (t *copyTracker) PrepareCandidate(candidate JsonValue) (*copyPrepared, error) {
	state := &copyState{status: copyStatusPrepared, baseRevision: t.revision, base: t.value, working: candidate, origin: map[identity]any{}, edited: true}
	t.register(state)
	prepared, err := materialize(state, t.owner)
	if err != nil {
		state.status = copyStatusAborted
		state.release()
		return nil, err
	}
	return prepared, nil
}

// Adopt installs a prepared revision with a pointer swap. It rejects a foreign, aborted, already adopted or stale preparation; adopting one revision makes every competing change and preparation stale.
func (t *copyTracker) Adopt(prepared *copyPrepared) error {
	if prepared == nil || prepared.owner != t.owner {
		return fmt.Errorf("Prepared change belongs to a different tracker")
	}
	state := prepared.state
	switch state.status {
	case copyStatusConsumed:
		return fmt.Errorf("Prepared change has already been used")
	case copyStatusAborted:
		return fmt.Errorf("Prepared change has been aborted")
	case copyStatusStale:
		return fmt.Errorf("Prepared change is stale")
	case copyStatusPrepared:
	default:
		return fmt.Errorf("Prepared change is not ready")
	}
	if state.baseRevision != t.revision || !copySameRoot(t.value, prepared.Base) {
		state.status = copyStatusStale
		state.release()
		return fmt.Errorf("Prepared change is stale")
	}
	t.value = prepared.Value
	state.status = copyStatusConsumed
	t.revision++
	t.invalidate(state)
	return nil
}

func copySameRoot(left, right JsonValue) bool {
	if isContainer(left) && isContainer(right) {
		return strictEqual(left, right)
	}
	return reflect.DeepEqual(left, right)
}

func (state *copyState) release() {
	state.base, state.working, state.origin = nil, nil, nil
}

func (t *copyTracker) register(state *copyState) {
	if len(t.live) >= 256 && len(t.live) == cap(t.live) {
		kept := t.live[:0]
		for _, reference := range t.live {
			if reference.Value() != nil {
				kept = append(kept, reference)
			}
		}
		clear(t.live[len(kept):])
		t.live = kept
	}
	t.live = append(t.live, weak.Make(state))
}

// invalidate makes every other open or prepared change stale and drops its working data in O(live changes).
func (t *copyTracker) invalidate(winner *copyState) {
	for _, reference := range t.live {
		other := reference.Value()
		if other == nil || other == winner {
			continue
		}
		if other.status == copyStatusOpen || other.status == copyStatusPrepared {
			other.status = copyStatusStale
		}
		other.release()
	}
	clear(t.live)
	t.live = t.live[:0]
}

func abort(state *copyState) {
	switch state.status {
	case copyStatusOpen, copyStatusPrepared:
		state.status = copyStatusAborted
		state.release()
	}
}

// State returns the editable working copy of the base revision: maps and slices to be edited in place, with whole values replaced through their parent. It is a private copy made on first use, so edits never reach the base. It fails once the change is prepared, aborted, or made stale by another adoption.
func (c *copyChange) State() (JsonValue, error) {
	if c.settled || c.state.status != copyStatusOpen {
		return nil, typeError("Cannot use a settled overlay")
	}
	if !c.state.edited {
		c.state.working, c.state.origin = cloneWithOrigin(c.state.base)
		c.state.edited = true
	}
	return c.state.working, nil
}

// Replace swaps the working copy's root for value, which the change takes ownership of; it is how a root that is not a container, or one built from scratch, is edited.
func (c *copyChange) Replace(value JsonValue) error {
	if _, err := c.State(); err != nil {
		return err
	}
	c.state.working = value
	return nil
}

// Prepare materializes the candidate revision and its operations without changing the tracker. The working copy must be strict JSON: container values, strings, booleans, finite numbers and nil. Anything else is rejected with an error and nothing is prepared. After Prepare the change is settled.
func (c *copyChange) Prepare() (*copyPrepared, error) {
	if c.settled {
		return nil, fmt.Errorf("Change has already been settled")
	}
	state := c.state
	if state.status != copyStatusOpen {
		return nil, typeError("Cannot use a settled overlay")
	}
	state.status = copyStatusPrepared
	c.settled = true
	owner := c.tracker.owner
	c.tracker = nil
	prepared, err := materialize(state, owner)
	if err != nil {
		state.status = copyStatusAborted
		state.release()
		return nil, err
	}
	return prepared, nil
}

// Abort discards the change. It is idempotent, and after Prepare it prevents adoption of the prepared result.
func (c *copyChange) Abort() {
	c.settled = true
	c.tracker = nil
	abort(c.state)
}

func materialize(state *copyState, owner *byte) (*copyPrepared, error) {
	base := state.base
	prepared := &copyPrepared{Base: base, Value: base, Ops: []Op{}, BaseRevision: state.baseRevision, owner: owner, state: state}
	if !state.edited {
		state.release()
		return prepared, nil
	}
	candidate, err := normalize(state.working)
	if err != nil {
		return nil, err
	}
	reconciled := (&reconciler{origin: state.origin, used: map[identity]struct{}{}}).reconcile(candidate, base)
	ops := DiffRevisions(base, reconciled)
	state.release()
	if len(ops) == 0 {
		return prepared, nil
	}
	prepared.Value, prepared.Ops = reconciled, ops
	return prepared, nil
}

// cloneWithOrigin deep-copies a revision and records, for every copied container, the container it came from, so equal containers can be restored to their original identity after editing.
func cloneWithOrigin(root JsonValue) (JsonValue, map[identity]any) {
	origin := map[identity]any{}
	var clone func(value any) any
	clone = func(value any) any {
		switch typed := value.(type) {
		case *chordjson.Object:
			out := chordjson.NewObject(typed.Len())
			for key, item := range typed.All() {
				out.Set(key, clone(item))
			}
			remember(origin, out, typed)
			return out
		case []any:
			out := make([]any, len(typed))
			for at, item := range typed {
				out[at] = clone(item)
			}
			remember(origin, out, typed)
			return out
		default:
			return value
		}
	}
	return clone(root), origin
}

func remember(origin map[identity]any, copied, source any) {
	if key, tracked := identityOf(copied); tracked {
		origin[key] = source
	}
}

// normalize validates the edited working copy as strict JSON and makes it alias-free: a container reached twice becomes two independent containers, Go integer and float kinds become float64, and a Go map[string]any becomes a *chordjson.Object with its keys in chordjson.MapOwnKeys order. It writes into a container only to replace a child, so a committed container the caller placed in the working copy is never written.
//
// It walks objects in own-key order, so the first invalid property is the one upstream reports (revision-validator.ts:26-33).
func normalize(root JsonValue) (JsonValue, error) {
	seen := map[identity]struct{}{}
	active := map[identity]struct{}{}
	var walk func(value any) (any, bool, error)
	walk = func(value any) (any, bool, error) {
		switch typed := value.(type) {
		case nil, bool, string:
			return typed, false, nil
		case *chordjson.Object, []any:
			return walkContainer(typed, seen, active, walk)
		case map[string]any:
			if typed == nil {
				return nil, false, typeError("Replicated state containers must be plain objects or arrays")
			}
			// The map itself is on the active path while its converted object is walked, so a cycle through it is still found.
			mapKey := identity{pointer: reflect.ValueOf(typed).Pointer(), length: -2}
			if _, cyclic := active[mapKey]; cyclic {
				return nil, false, typeError("Replicated state cannot contain cycles")
			}
			active[mapKey] = struct{}{}
			defer delete(active, mapKey)
			object := chordjson.NewObject(len(typed))
			for _, name := range chordjson.MapOwnKeys(typed) {
				object.Set(name, typed[name])
			}
			next, _, err := walkContainer(object, seen, active, walk)
			return next, true, err
		}
		n, err := normalizeNumber(value)
		if _, isFloat := value.(float64); isFloat {
			return n, false, err
		}
		return n, true, err
	}
	next, _, err := walk(root)
	if err != nil {
		return nil, err
	}
	return next, nil
}

func walkContainer(value any, seen, active map[identity]struct{}, walk func(any) (any, bool, error)) (any, bool, error) {
	key, tracked := identityOf(value)
	if tracked {
		if _, cyclic := active[key]; cyclic {
			return nil, false, typeError("Replicated state cannot contain cycles")
		}
		if _, aliased := seen[key]; aliased {
			// Placing one value at several paths yields independent containers.
			copied, err := chordjson.Copy(value)
			return copied, true, err
		}
		seen[key] = struct{}{}
		active[key] = struct{}{}
		defer delete(active, key)
	}
	switch typed := value.(type) {
	case *chordjson.Object:
		if typed == nil {
			return nil, false, typeError("Replicated state containers must be plain objects or arrays")
		}
		for name, item := range typed.All() {
			next, changed, err := walk(item)
			if err != nil {
				return nil, false, err
			}
			if changed {
				typed.Set(name, next)
			}
		}
		return typed, false, nil
	default:
		items := value.([]any)
		for at, item := range items {
			next, changed, err := walk(item)
			if err != nil {
				return nil, false, err
			}
			if changed {
				items[at] = next
			}
		}
		return items, false, nil
	}
}

func normalizeNumber(value any) (any, error) {
	n, ok := number(value)
	if !ok {
		switch reflect.ValueOf(value).Kind() {
		case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array, reflect.Pointer, reflect.Interface:
			return nil, typeError("Replicated state containers must be plain objects or arrays")
		}
		return nil, typeError("Replicated state values must be strict JSON")
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, typeError("Replicated state values must be strict JSON")
	}
	return n, nil
}

// reconciler restores base identity: a working container equal to the container it was copied from, or to the base container at the same position, becomes that container again. Containers moved within an array keep their origin, so a shifted row is still recognized.
//
// A base container is restored at most once, so the result stays alias-free: when a working container is aliased or moved, only the first claimant gets the base container and later ones keep their working copy.
type reconciler struct {
	origin map[identity]any
	used   map[identity]struct{}
}

func (r *reconciler) claimed(hint JsonValue) bool {
	key, tracked := identityOf(hint)
	if !tracked {
		return false
	}
	_, used := r.used[key]
	return used
}

func (r *reconciler) claim(hint JsonValue) {
	if key, tracked := identityOf(hint); tracked {
		r.used[key] = struct{}{}
	}
}

func (r *reconciler) reconcile(work, positional JsonValue) JsonValue {
	hint := positional
	if key, tracked := identityOf(work); tracked {
		if source, copied := r.origin[key]; copied {
			hint = source
		}
	}
	if isContainer(hint) && r.claimed(hint) {
		hint = nil
	}
	switch typed := work.(type) {
	case *chordjson.Object:
		counterpart, _ := hint.(*chordjson.Object)
		same := counterpart != nil && counterpart.Len() == typed.Len()
		for key, item := range typed.All() {
			base, exists := counterpart.Get(key)
			next := r.reconcile(item, valueIf(base, exists))
			if !strictEqual(next, item) {
				typed.Set(key, next)
			}
			same = same && exists && strictEqual(next, base)
		}
		if same {
			r.claim(counterpart)
			return counterpart
		}
		return typed
	case []any:
		counterpart, _ := hint.([]any)
		for at, item := range typed {
			var base any
			exists := at < len(counterpart)
			if exists {
				base = counterpart[at]
			}
			if next := r.reconcile(item, valueIf(base, exists)); !strictEqual(next, item) {
				typed[at] = next
			}
		}
		if counterpart == nil {
			return typed
		}
		r.matchMoved(typed, counterpart)
		same := len(counterpart) == len(typed)
		for at := 0; same && at < len(typed); at++ {
			same = strictEqual(typed[at], counterpart[at])
		}
		if same {
			r.claim(counterpart)
			return counterpart
		}
		return typed
	default:
		return work
	}
}

// matchMoved restores, by deep equality, the elements that the positional pass left as working copies and that equal a base element elsewhere in the array: an element that moved, or one rebuilt by a caller that kept no origin (a decoded typed value). Each base element is restored at most once.
func (r *reconciler) matchMoved(work, base []any) {
	var index map[uint64][]int
	for at, item := range work {
		if !isContainer(item) || r.claimed(item) {
			continue
		}
		if index == nil {
			index = map[uint64][]int{}
			for position, candidate := range base {
				if isContainer(candidate) {
					key := hashValue(candidate)
					index[key] = append(index[key], position)
				}
			}
		}
		for _, position := range index[hashValue(item)] {
			candidate := base[position]
			if r.claimed(candidate) || !equalJson(item, candidate) {
				continue
			}
			work[at] = candidate
			r.claim(candidate)
			break
		}
	}
}

func valueIf(value any, exists bool) any {
	if exists {
		return value
	}
	return nil
}

// DiffCandidate returns the operations that turn base into candidate, a complete revision the caller built (typed state edits a decoded copy instead of a draft), and the candidate restored to base's container identity wherever it is deeply equal, so array alignment matches moved elements by identity. The candidate is validated as strict JSON and normalized; an invalid candidate returns an error. The result takes ownership of candidate.
func DiffCandidate(base, candidate JsonValue) ([]Op, JsonValue, error) {
	prepared, err := trackCopy(base).PrepareCandidate(candidate)
	if err != nil {
		return nil, nil, err
	}
	defer prepared.Abort()
	return prepared.Ops, prepared.Value, nil
}
