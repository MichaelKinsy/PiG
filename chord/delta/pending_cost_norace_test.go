//go:build !race

package delta

import "testing"

// upstream: delta-pending.test.ts "computes one path key per leaf op once folding, none for one batch or structural ops". The upstream case counts JSON.stringify calls, a JavaScript instrumentation detail; the Go key is a strings.Builder string, so the same property is an allocation count: a consumer that keeps up (one batch pending) builds no path key, and so allocates nothing.
func TestPendingOpsBuildNoPathKeyForOneBatch(t *testing.T) {
	leaves := []Op{
		{"a", []any{"entries", 0, "text"}, "a"},
		{"s", []any{"meta", "tokens"}, float64(1)},
	}
	var queue PendingOps
	if allocs := testing.AllocsPerRun(100, func() {
		queue.Push(leaves)
		queue.Take()
	}); allocs != 0 {
		t.Fatalf("pushing and taking one batch allocated %v times, want 0 (no path key for a batch kept by reference)", allocs)
	}
	structural := []Op{{"p", []any{"entries"}, float64(1), float64(0), []any{"x"}}, {"d", []any{"x"}}}
	if allocs := testing.AllocsPerRun(100, func() {
		queue.Push(structural)
		queue.Take()
	}); allocs != 0 {
		t.Fatalf("pushing and taking one structural batch allocated %v times, want 0", allocs)
	}
}
