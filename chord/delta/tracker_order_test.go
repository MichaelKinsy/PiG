package delta

import (
	"testing"
)

// Emission-order rules of tracker.ts that the randomized differential scripts reach rarely or not at all. Each expected batch was recorded from the pinned tracker.ts with the same edits.

// tracker.ts setArrayIndex deletes a base override when the base element's own value is written back, so a later write of that element is appended to baseOverrides after the other overrides: Pi records [["s",["a",1],20],["s",["a",0],30]].
func TestTrackerRestoringABaseElementDropsItsOverridePosition(t *testing.T) {
	tracker := Track(JsonObjectOf("a", []any{1.0, 2.0, 3.0}))
	change := tracker.BeginChange()
	array := change.State().Array("a")
	for _, write := range []struct {
		index int
		value float64
	}{{0, 10}, {1, 20}, {0, 1}, {0, 30}} {
		if err := array.Set(write.index, write.value); err != nil {
			t.Fatal(err)
		}
	}
	if got := opsText(t, mustPrepare(t, change).Ops()); got != `[["s",["a",1],20],["s",["a",0],30]]` {
		t.Fatalf("ops %s", got)
	}

	change = tracker.BeginChange()
	array = change.State().Array("a")
	if err := array.Set(0, 10.0); err != nil {
		t.Fatal(err)
	}
	if err := array.Set(0, 1.0); err != nil {
		t.Fatal(err)
	}
	if got := opsText(t, mustPrepare(t, change).Ops()); got != `[]` {
		t.Fatalf("a restored element must emit nothing: %s", got)
	}
}

// tracker.ts deleteProperty returns before markDirty when the key is absent, so deleting a missing key does not move its node ahead in the dirty order: Pi records [["s",["x","k"],2],["s",["y","k"],2]].
func TestTrackerDeletingAnAbsentKeyDoesNotDirtyTheNode(t *testing.T) {
	tracker := Track(JsonObjectOf("x", JsonObjectOf("k", 1.0), "y", JsonObjectOf("k", 1.0)))
	change := tracker.BeginChange()
	state := change.State()
	state.Object("y").Delete("missing")
	if err := state.Object("x").Set("k", 2.0); err != nil {
		t.Fatal(err)
	}
	if err := state.Object("y").Set("k", 2.0); err != nil {
		t.Fatal(err)
	}
	if got := opsText(t, mustPrepare(t, change).Ops()); got != `[["s",["x","k"],2],["s",["y","k"],2]]` {
		t.Fatalf("ops %s", got)
	}
}

// Reorder applies an "m" permutation: the element now at position i is the one that was at permutation[i]. The recorded batch is the permutation tracker.ts records for the same order (reverse and sort produce it there), and it replays to the draft's value.
func TestArrayReorderRecordsThePermutation(t *testing.T) {
	tracker := Track(JsonObjectOf("a", []any{"x", "y", "z"}))
	base := tracker.Value()
	change := tracker.BeginChange()
	if err := change.State().Array("a").Reorder([]int{2, 0, 1}); err != nil {
		t.Fatal(err)
	}
	prepared := mustPrepare(t, change)
	expectJSONText(t, prepared.Value(), `{"a":["z","x","y"]}`)
	if got := opsText(t, prepared.Ops()); got != `[["m",["a"],[2,0,1]]]` {
		t.Fatalf("ops %s", got)
	}
	expectJSONText(t, replayThroughWire(t, base, prepared.Ops()), `{"a":["z","x","y"]}`)

	change = tracker.BeginChange()
	if err := change.State().Array("a").Reorder([]int{0, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if got := opsText(t, mustPrepare(t, change).Ops()); got != `[]` {
		t.Fatalf("the identity permutation must emit nothing: %s", got)
	}
}

func TestArrayReorderRejectsAnInvalidPermutationWithoutChangingTheDraft(t *testing.T) {
	for _, permutation := range [][]int{{0, 1}, {0, 1, 2, 3}, {0, 0, 1}, {0, 1, 3}, {-1, 0, 1}} {
		tracker := Track(JsonObjectOf("a", []any{"x", "y", "z"}))
		change := tracker.BeginChange()
		if err := change.State().Array("a").Reorder(permutation); err == nil {
			t.Fatalf("permutation %v was accepted", permutation)
		}
		prepared := mustPrepare(t, change)
		if got := opsText(t, prepared.Ops()); got != `[]` {
			t.Fatalf("permutation %v changed the draft: %s", permutation, got)
		}
	}
}
