package state_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
)

type linkstateDependent struct{ invalidated int }

func (d *linkstateDependent) Invalidate()                                { d.invalidated++ }
func (d *linkstateDependent) SourceRetired(map[state.Dependent]struct{}) {}

// A dependent set is a JS Set in insertion order: adding a member twice keeps one entry (watchable.ts `dependents`).
func TestDependentSetHoldsEachMemberOnce(t *testing.T) {
	var set state.DependentSet
	first, second := &linkstateDependent{}, &linkstateDependent{}
	set.Add(first)
	set.Add(second)
	set.Add(first)
	if set.Len() != 2 {
		t.Fatalf("Len = %d, want 2", set.Len())
	}
	snapshot := set.Snapshot()
	if len(snapshot) != 2 || snapshot[0] != state.Dependent(first) || snapshot[1] != state.Dependent(second) {
		t.Fatalf("snapshot = %v, want [first second]", snapshot)
	}
	if !set.Delete(first) || set.Delete(first) || set.Len() != 1 {
		t.Fatalf("delete of first: Len = %d", set.Len())
	}
}
