//go:build !race

package jsonstringify

import "testing"

// Canonical text with large objects is copied with at most the spilled key stack and the sort scratch of a large object (PE-F9).
// The race detector's instrumentation adds allocations, so this bound runs without it; TestIsCanonicalAcceptsLargeObjects checks
// the behavior in both modes.
func TestAppendCanonicalOfLargeObjectsAllocatesAtMostTwice(t *testing.T) {
	for _, text := range largeCanonicalObjects() {
		dst, raw := make([]byte, 0, len(text)), []byte(text)
		if allocs := testing.AllocsPerRun(20, func() { dst, _ = AppendCanonical(dst[:0], raw) }); allocs > 2 {
			t.Fatalf("AppendCanonical of canonical text of %d bytes allocates %v times, want at most 2", len(text), allocs)
		}
	}
}
