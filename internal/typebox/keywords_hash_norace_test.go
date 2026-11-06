//go:build !race

package typebox

import "testing"

// uniqueItems hashes strings and orders object members without copying their text.
func TestHashValueDoesNotAllocateForStringsAndSmallObjects(t *testing.T) {
	text := "item name with é and 日本語"
	if allocs := testing.AllocsPerRun(100, func() { _ = hashValue(text) }); allocs != 0 {
		t.Fatalf("hashValue of a string allocates %v times, want 0", allocs)
	}
	// An object allocates its sorted member names once, and nothing per comparison.
	object := map[string]any{"id": 1.0, "name": "item", "kind": "k", "é": "ü", "日本": nil, "z": true}
	if allocs := testing.AllocsPerRun(100, func() { _ = hashValue(object) }); allocs > 1 {
		t.Fatalf("hashValue of an object allocates %v times, want at most 1", allocs)
	}
}
