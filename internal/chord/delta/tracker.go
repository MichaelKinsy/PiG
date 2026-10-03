package delta

import (
	"fmt"
	"math"
	"reflect"
	"weak"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/src/delta/tracker.ts.
//
// Go mapping: upstream hands out a copy-on-write Proxy overlay whose property writes are recorded as they happen. Go values cannot be intercepted, so Change.State returns a private deep copy of the base revision, built on first use, that the caller edits with ordinary map and slice operations. Prepare validates and de-aliases the edited copy, restores the identity of every container that ended up equal to its base counterpart, and diffs the base against the result. Everything observable about the contract is kept: the candidate is a new immutable revision sharing unchanged containers with the base, no-op edits normalize to an empty batch and the previous revision, a competing adoption makes every other open or prepared change stale, and settled changes release their data. What cannot be kept is Proxy behavior: handles cannot be revoked, held handles do not follow array reindexing, and placement happens by reference. Prepare takes the validating copy instead.

// Tracker owns the revision sequence of one strict-JSON root. One tracker is one revision sequence; delivery order and persistence belong to the surrounding protocol.
type Tracker struct {
	owner    *byte
	value    JsonValue
	revision int
	live     []weak.Pointer[change]
}

type status uint8

const (
	statusOpen status = iota
	statusPrepared
	statusConsumed
	statusAborted
	statusStale
)

// change is the shared state of one Change and the Prepared it produced. It holds no revision data after it settles, so a retained settled Change or Prepared never keeps the base alive through it.
type change struct {
	status       status
	baseRevision int
	base         JsonValue
	working      JsonValue
	origin       map[identity]any
	edited       bool // the working copy exists
}

// Prepared is one fully materialized candidate revision. Base, Value and Ops, including op paths and payloads, are immutable by contract, and payloads may be the same containers as parts of Value. An empty Ops means Value is Base.
type Prepared struct {
	Base         JsonValue
	Value        JsonValue
	Ops          []Op
	BaseRevision int

	owner *byte
	state *change
}

// Abort prevents adoption. The candidate stays readable. Abort is idempotent.
func (p *Prepared) Abort() { abort(p.state) }

// Change is one open transaction over the tracker's latest revision.
type Change struct {
	tracker *Tracker
	state   *change
	settled bool
}

// Track takes immutable ownership of an alias-free strict-JSON root in O(1). The root must not be mutated again.
func Track(initial JsonValue) *Tracker {
	return &Tracker{owner: new(byte), value: initial}
}

// Value is the latest adopted revision.
func (t *Tracker) Value() JsonValue { return t.value }

// Revision counts adoptions; adopting a no-op still advances it.
func (t *Tracker) Revision() int { return t.revision }

// BeginChange opens a transaction over the latest revision without modifying it. The transaction may stay open across blocking calls; it ends with Prepare or Abort.
func (t *Tracker) BeginChange() *Change {
	state := &change{status: statusOpen, baseRevision: t.revision, base: t.value}
	t.register(state)
	return &Change{tracker: t, state: state}
}

// PrepareReplace is a whole-root operation, not a diff. It takes immutable ownership of value in O(1) and never walks it, except to compare it with the current root. When deeply equal to the current root the batch is empty and the current root is kept, otherwise it is [["r", value]].
func (t *Tracker) PrepareReplace(value JsonValue) *Prepared {
	state := &change{status: statusPrepared, baseRevision: t.revision}
	t.register(state)
	prepared := &Prepared{Base: t.value, Ops: []Op{}, BaseRevision: t.revision, owner: t.owner, state: state}
	if equalJson(t.value, value) {
		prepared.Value = t.value
		return prepared
	}
	prepared.Value = value
	prepared.Ops = []Op{{"r", value}}
	return prepared
}

// PrepareCandidate prepares a revision from a complete candidate the caller built, for callers that edit a decoded copy of the value (typed state) instead of the working copy of a Change. The candidate is validated, restored to the base's container identity where it is deeply equal, and diffed against the base; the tracker takes ownership of it. An invalid candidate fails without preparing anything.
func (t *Tracker) PrepareCandidate(candidate JsonValue) (*Prepared, error) {
	state := &change{status: statusPrepared, baseRevision: t.revision, base: t.value, working: candidate, origin: map[identity]any{}, edited: true}
	t.register(state)
	prepared, err := materialize(state, t.owner)
	if err != nil {
		state.status = statusAborted
		state.release()
		return nil, err
	}
	return prepared, nil
}

// Adopt installs a prepared revision with a pointer swap. It rejects a foreign, aborted, already adopted or stale preparation; adopting one revision makes every competing change and preparation stale.
func (t *Tracker) Adopt(prepared *Prepared) error {
	if prepared == nil || prepared.owner != t.owner {
		return fmt.Errorf("Prepared change belongs to a different tracker")
	}
	state := prepared.state
	switch state.status {
	case statusConsumed:
		return fmt.Errorf("Prepared change has already been used")
	case statusAborted:
		return fmt.Errorf("Prepared change has been aborted")
	case statusStale:
		return fmt.Errorf("Prepared change is stale")
	case statusPrepared:
	default:
		return fmt.Errorf("Prepared change is not ready")
	}
	if state.baseRevision != t.revision || !sameRoot(t.value, prepared.Base) {
		state.status = statusStale
		state.release()
		return fmt.Errorf("Prepared change is stale")
	}
	t.value = prepared.Value
	state.status = statusConsumed
	t.revision++
	t.invalidate(state)
	return nil
}

func sameRoot(left, right JsonValue) bool {
	if isContainer(left) && isContainer(right) {
		return strictEqual(left, right)
	}
	return reflect.DeepEqual(left, right)
}

func (state *change) release() {
	state.base, state.working, state.origin = nil, nil, nil
}

func (t *Tracker) register(state *change) {
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
func (t *Tracker) invalidate(winner *change) {
	for _, reference := range t.live {
		other := reference.Value()
		if other == nil || other == winner {
			continue
		}
		if other.status == statusOpen || other.status == statusPrepared {
			other.status = statusStale
		}
		other.release()
	}
	clear(t.live)
	t.live = t.live[:0]
}

func abort(state *change) {
	switch state.status {
	case statusOpen, statusPrepared:
		state.status = statusAborted
		state.release()
	}
}

// State returns the editable working copy of the base revision: maps and slices to be edited in place, with whole values replaced through their parent. It is a private copy made on first use, so edits never reach the base. It fails once the change is prepared, aborted, or made stale by another adoption.
func (c *Change) State() (JsonValue, error) {
	if c.settled || c.state.status != statusOpen {
		return nil, typeError("Cannot use a settled overlay")
	}
	if !c.state.edited {
		c.state.working, c.state.origin = cloneWithOrigin(c.state.base)
		c.state.edited = true
	}
	return c.state.working, nil
}

// Replace swaps the working copy's root for value, which the change takes ownership of; it is how a root that is not a container, or one built from scratch, is edited.
func (c *Change) Replace(value JsonValue) error {
	if _, err := c.State(); err != nil {
		return err
	}
	c.state.working = value
	return nil
}

// Prepare materializes the candidate revision and its operations without changing the tracker. The working copy must be strict JSON: container values, strings, booleans, finite numbers and nil. Anything else is rejected with an error and nothing is prepared. After Prepare the change is settled.
func (c *Change) Prepare() (*Prepared, error) {
	if c.settled {
		return nil, fmt.Errorf("Change has already been settled")
	}
	state := c.state
	if state.status != statusOpen {
		return nil, typeError("Cannot use a settled overlay")
	}
	state.status = statusPrepared
	c.settled = true
	owner := c.tracker.owner
	c.tracker = nil
	prepared, err := materialize(state, owner)
	if err != nil {
		state.status = statusAborted
		state.release()
		return nil, err
	}
	return prepared, nil
}

// Abort discards the change. It is idempotent, and after Prepare it prevents adoption of the prepared result.
func (c *Change) Abort() {
	c.settled = true
	c.tracker = nil
	abort(c.state)
}

func materialize(state *change, owner *byte) (*Prepared, error) {
	base := state.base
	prepared := &Prepared{Base: base, Value: base, Ops: []Op{}, BaseRevision: state.baseRevision, owner: owner, state: state}
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
		case map[string]any:
			out := make(map[string]any, len(typed))
			for key, item := range typed {
				out[key] = clone(item)
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

// normalize validates the edited working copy as strict JSON and makes it alias-free: a container reached twice becomes two independent containers, Go integer and float kinds become float64. It writes into a container only to replace a child, so a committed container the caller placed in the working copy is never written.
func normalize(root JsonValue) (JsonValue, error) {
	seen := map[identity]struct{}{}
	active := map[identity]struct{}{}
	var walk func(value any) (any, bool, error)
	walk = func(value any) (any, bool, error) {
		switch typed := value.(type) {
		case nil, bool, string:
			return typed, false, nil
		case map[string]any, []any:
			return walkContainer(typed, seen, active, walk)
		}
		n, err := normalizeNumber(value)
		if _, isFloat := value.(float64); isFloat {
			return n, false, err
		}
		return n, true, err
	}
	next, _, err := walk(root)
	return next, err
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
	case map[string]any:
		if typed == nil {
			return nil, false, typeError("Replicated state containers must be plain objects or arrays")
		}
		for name, item := range typed {
			next, changed, err := walk(item)
			if err != nil {
				return nil, false, err
			}
			if changed {
				typed[name] = next
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
			return nil, typeError("Replicated state containers must be plain objects or arrays, not %T", value)
		}
		return nil, typeError("Replicated state values must be strict JSON, not %T", value)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, typeError("Replicated state values must be strict JSON: non-finite number")
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
	case map[string]any:
		counterpart, _ := hint.(map[string]any)
		same := counterpart != nil && len(counterpart) == len(typed)
		for key, item := range typed {
			base, exists := counterpart[key]
			next := r.reconcile(item, valueIf(base, exists))
			if !strictEqual(next, item) {
				typed[key] = next
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
