package codingagent

import (
	"runtime"
	"slices"
	"sync"
	"testing"
)

// parallelOverWeighted hands every item to exactly one part, in order, and splits a session of few heavy records across goroutines, which the line
// count alone leaves on one.
func TestParallelOverWeightedSplitsByRecordBytes(t *testing.T) {
	const heavy = 45 << 10 // a tool output of tens of kilobytes
	for _, tc := range []struct {
		name        string
		weights     []int64
		wantAtLeast int
	}{
		{"few heavy records", slices.Repeat([]int64{heavy}, 600), 4},
		{"many light records", slices.Repeat([]int64{100}, 5000), 2},
		{"one heavy record among light ones", append(slices.Repeat([]int64{100}, 300), 8<<20), 2},
		{"small read stays whole", slices.Repeat([]int64{100}, 50), 1},
		{"resident entries only", slices.Repeat([]int64{0}, 900), 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]int, len(tc.weights))
			for i := range items {
				items[i] = i
			}
			var mu sync.Mutex
			var parts [][]int
			parallelOverWeighted(items, func(item int) int64 { return tc.weights[item] }, parallelReadMinBytes, func(part []int) {
				mu.Lock()
				defer mu.Unlock()
				parts = append(parts, slices.Clone(part))
			})
			slices.SortFunc(parts, func(a, b []int) int { return a[0] - b[0] })
			var joined []int
			for _, part := range parts {
				if len(part) == 0 {
					t.Fatal("an empty part")
				}
				joined = append(joined, part...)
			}
			if !slices.Equal(joined, items) {
				t.Fatalf("parts do not cover the items once, in order: %d parts, %d items joined from %d", len(parts), len(joined), len(items))
			}
			// The split never exceeds the processors the run may use, and fills as many as the cost allows.
			if limit := runtime.GOMAXPROCS(0); len(parts) > limit {
				t.Fatalf("%d parts exceed %d processors", len(parts), limit)
			}
			if want := min(tc.wantAtLeast, runtime.GOMAXPROCS(0)); len(parts) < want {
				t.Fatalf("%d parts, want at least %d", len(parts), want)
			}
		})
	}
}
