//go:build !race

package compaction

import "testing"

// The race detector adds allocations of its own that grow with the data, so the allocation count is measured without it.

// TestPrepareCompactionDoesNotCopyToolSchemas: preparing reads the declared tools for its estimate and copies none of them.
func TestPrepareCompactionDoesNotCopyToolSchemas(t *testing.T) {
	allocs := func(properties int) float64 {
		entries := borrowSession(t, 3, properties)
		return testing.AllocsPerRun(10, func() {
			if PrepareCompaction(entries, borrowSettings) == nil {
				t.Fatal("nothing prepared")
			}
		})
	}
	// Two allocations of slack: sizing a growing buffer (the stringified length of the declarations) can differ by one step; a copy
	// of the schemas differs by hundreds.
	if small, large := allocs(2), allocs(60); large-small > 2 {
		t.Errorf("allocations per preparation: %v with 2-property schemas, %v with 60-property ones: the schemas are copied or decoded again", small, large)
	}
}
