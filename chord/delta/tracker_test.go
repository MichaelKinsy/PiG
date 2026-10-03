package delta

import (
	"errors"
	"math"
	"testing"
)

func mustPrepare(t *testing.T, change *Change) *Prepared {
	t.Helper()
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return prepared
}

func TestTrackerNormalizesRestoredWritesAndKeepsStructuralNoOps(t *testing.T) {
	tracker := Track(map[string]any{"items": []any{"a", "b"}, "nested": map[string]any{"count": 0.0}})
	change := tracker.BeginChange()
	state := change.State()
	if err := state.Object("nested").Set("count", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Array("items").Push("z"); err != nil {
		t.Fatal(err)
	}
	state.Array("items").Pop()
	prepared := mustPrepare(t, change)
	if len(prepared.Ops()) != 0 || !sameContainer(prepared.Value(), prepared.Base()) {
		t.Fatalf("restored writes must normalize to an empty batch: %v", prepared.Ops())
	}

	change = tracker.BeginChange()
	items := change.State().Array("items")
	first := items.Shift()
	if _, err := items.Unshift(first); err != nil {
		t.Fatal(err)
	}
	prepared = mustPrepare(t, change)
	if len(prepared.Ops()) == 0 {
		t.Fatal("a structural array edit emits an exact nonempty batch")
	}
	if !equalTrustedJSON(prepared.Value(), prepared.Base()) || sameContainer(prepared.Value(), prepared.Base()) {
		t.Fatalf("structural no-op: value %v base %v", prepared.Value(), prepared.Base())
	}
	replayed, err := ApplyImmutable(prepared.Base(), prepared.Ops())
	if err != nil || !equalTrustedJSON(replayed, prepared.Value()) {
		t.Fatalf("replay %v err %v", replayed, err)
	}
}

func TestTrackerSharesSetPayloadsAndStructure(t *testing.T) {
	tracker := Track(map[string]any{"items": []any{"a"}, "other": map[string]any{"label": "x"}})
	change := tracker.BeginChange()
	if err := change.State().Set("other", map[string]any{"label": "y"}); err != nil {
		t.Fatal(err)
	}
	prepared := mustPrepare(t, change)
	if len(prepared.Ops()) != 1 || prepared.Ops()[0][0] != "s" {
		t.Fatalf("ops %v", prepared.Ops())
	}
	if !sameContainer(prepared.Ops()[0][2], prepared.Value()["other"]) {
		t.Fatal("set payload must be the revision's container")
	}
	if !sameContainer(prepared.Value()["items"], prepared.Base()["items"]) {
		t.Fatal("unchanged subtrees are structurally shared")
	}
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
	if !sameContainer(tracker.Value(), prepared.Value()) {
		t.Fatal("adoption swaps the root pointer")
	}
}

func TestTrackerRevokesDraftsAndRejectsNonStrictPlacements(t *testing.T) {
	tracker := Track(map[string]any{"items": []any{}})
	change := tracker.BeginChange()
	state := change.State()
	items := state.Array("items")
	if _, err := items.Push(1, math.NaN()); !errors.Is(err, ErrNotStrictJSON) {
		t.Fatalf("NaN placement: %v", err)
	}
	if items.Len() != 0 {
		t.Fatal("a rejected push inserts nothing")
	}
	if err := state.Set("f", func() {}); !errors.Is(err, ErrNotStrictJSON) {
		t.Fatalf("func placement: %v", err)
	}
	mustPrepare(t, change)
	assertRevoked(t, func() { state.Get("items") })
	assertRevoked(t, func() { _ = state.Set("x", 1) })
	assertRevoked(t, func() { items.Len() })
}

func TestTrackerFoldsLargeBatchesIntoRootReplacement(t *testing.T) {
	initial := map[string]any{}
	for index := range 4_100 {
		initial[string(rune('a'+index%26))+string(rune(index))] = 0.0
	}
	tracker := Track(initial)
	change := tracker.BeginChange()
	state := change.State()
	for _, key := range state.Keys() {
		if err := state.Set(key, 1); err != nil {
			t.Fatal(err)
		}
	}
	prepared := mustPrepare(t, change)
	if len(prepared.Ops()) != 1 || prepared.Ops()[0][0] != "r" {
		t.Fatalf("expected one root replacement, got %d ops", len(prepared.Ops()))
	}
}

func assertRevoked(t *testing.T, use func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if err, ok := recovered.(error); !ok || !errors.Is(err, ErrRevoked) {
			t.Fatalf("expected ErrRevoked panic, got %v", recovered)
		}
	}()
	use()
}

// Emptying an array through any removal path leaves it empty (Array.prototype.splice semantics) and records the
// removal; an empty entry list must not read as an untouched array.
func TestTrackerEmptiesArraysThroughEveryRemovalPath(t *testing.T) {
	for name, remove := range map[string]func(*Array){
		"splice": func(items *Array) {
			if _, err := items.Splice(0, items.Len()); err != nil {
				t.Fatal(err)
			}
		},
		"pop":    func(items *Array) { items.Pop() },
		"shift":  func(items *Array) { items.Shift() },
		"setLen": func(items *Array) { items.SetLen(0) },
	} {
		t.Run(name, func(t *testing.T) {
			tracker := Track(map[string]any{"items": []any{"x"}})
			change := tracker.BeginChange()
			items := change.State().Array("items")
			remove(items)
			if items.Len() != 0 || len(items.Snapshot()) != 0 {
				t.Fatalf("len %d snapshot %v", items.Len(), items.Snapshot())
			}
			if _, err := items.Push("y"); err != nil {
				t.Fatal(err)
			}
			items.Pop()
			prepared := mustPrepare(t, change)
			value := prepared.Value()["items"].([]any)
			if len(value) != 0 || len(prepared.Ops()) == 0 {
				t.Fatalf("value %v ops %v", value, prepared.Ops())
			}
			replayed, err := ApplyImmutable(prepared.Base(), prepared.Ops())
			if err != nil || !equalTrustedJSON(replayed, prepared.Value()) {
				t.Fatalf("replay %v err %v", replayed, err)
			}
		})
	}
}

// tracker.ts:1637-1645 and :1814: a mutation under a reserved key (__proto__, constructor, prototype) folds into one
// whole-value set at the nearest non-reserved ancestor; a reserved write at the root folds into a root replacement.
func TestTrackerFoldsReservedKeyMutationsAtTheNearestSafeAncestor(t *testing.T) {
	cases := []struct {
		name   string
		base   map[string]any
		mutate func(*Object) error
		want   []Op
	}{
		{"reserved write", map[string]any{"tools": map[string]any{}, "total": 0.0},
			func(state *Object) error { return state.Object("tools").Set("constructor", map[string]any{"n": 1}) },
			[]Op{{"s", []any{"tools"}, map[string]any{"constructor": map[string]any{"n": 1.0}}}}},
		{"reserved delete", map[string]any{"tools": map[string]any{"__proto__": 1.0, "x": 2.0}},
			func(state *Object) error { state.Object("tools").Delete("__proto__"); return nil },
			[]Op{{"s", []any{"tools"}, map[string]any{"x": 2.0}}}},
		{"edit below a reserved segment", map[string]any{"tools": map[string]any{"prototype": map[string]any{"n": 1.0}}},
			func(state *Object) error { return state.Object("tools").Object("prototype").Set("n", 2) },
			[]Op{{"s", []any{"tools"}, map[string]any{"prototype": map[string]any{"n": 2.0}}}}},
		{"reserved root write", map[string]any{"a": 1.0},
			func(state *Object) error { return state.Set("__proto__", "x") },
			[]Op{{"r", map[string]any{"a": 1.0, "__proto__": "x"}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tracker := Track(test.base)
			change := tracker.BeginChange()
			if err := test.mutate(change.State()); err != nil {
				t.Fatal(err)
			}
			prepared := mustPrepare(t, change)
			if !equalTrustedJSON(opsAsJSON(prepared.Ops()), opsAsJSON(test.want)) {
				t.Fatalf("ops %v, want %v", prepared.Ops(), test.want)
			}
			replayed, err := ApplyImmutable(prepared.Base(), prepared.Ops())
			if err != nil || !equalTrustedJSON(replayed, prepared.Value()) {
				t.Fatalf("replay %v err %v", replayed, err)
			}
		})
	}
}

func opsAsJSON(ops []Op) []any {
	result := make([]any, len(ops))
	for index, op := range ops {
		result[index] = []any(op)
	}
	return result
}
