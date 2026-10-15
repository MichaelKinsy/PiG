//go:build !race

// The race detector instruments allocations, so these allocation ratchets run without it, as the repository's other
// allocation ratchets do.

package agent

import "testing"

// A turn whose loadout did not change must cost a fixed number of allocations: no copy of a schema, no marshaling of one. The
// ceiling is checked against two independent denominators, the absolute budget measured after the change (56 allocations at 12
// tools and 40 messages, against 4,418 before it) and the requirement that a tenfold larger schema costs no more.
func TestDeclareToolChangesCostsNothingPerSchemaNodeWhenTheLoadoutIsUnchanged(t *testing.T) {
	measure := func(properties int) float64 {
		tools, committed := steadyToolFixtureWithProperties(12, 40, properties)
		return testing.AllocsPerRun(20, func() {
			if got := declareToolChanges(tools, committed, nil); len(got) != 0 {
				t.Fatalf("steady state declared %d messages", len(got))
			}
		})
	}
	small, large := measure(2), measure(40)
	if large > small {
		t.Fatalf("allocations grew with schema size: %v at 2 properties per tool, %v at 40", small, large)
	}
	const budget = 80
	if small > budget {
		t.Fatalf("declareToolChanges allocates %v times per steady turn, budget %d", small, budget)
	}
}
