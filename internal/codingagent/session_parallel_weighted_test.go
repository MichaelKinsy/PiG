package codingagent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// parallelOverWeighted hands every item to exactly one part, in order, and splits a session of few heavy records across goroutines, which the line
// count alone leaves on one. The split depends on the processors the run may use only in how many parts it makes, never in whether the cost
// allows more than one: each case is run at the widths of a small runner and of a large host.
func TestParallelOverWeightedSplitsByRecordBytes(t *testing.T) {
	for _, procs := range []int{1, 2, 3, 4, 16} {
		t.Run(fmt.Sprintf("procs=%d", procs), func(t *testing.T) {
			defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(procs))
			testParallelOverWeightedSplits(t)
		})
	}
}

func testParallelOverWeightedSplits(t *testing.T) {
	t.Helper()
	const heavy = 45 << 10 // a tool output of tens of kilobytes
	for _, tc := range []struct {
		name        string
		weights     []int64
		wantAtLeast int
	}{
		{"few heavy records", slices.Repeat([]int64{heavy}, 600), 4},
		{"many light records", slices.Repeat([]int64{100}, 5000), 2},
		{"one heavy record among light ones", append(slices.Repeat([]int64{100}, 300), 8<<20), 2},
		{"heavy record first, light ones after", append([]int64{8 << 20}, slices.Repeat([]int64{100}, 300)...), 2},
		{"light records around one heavy", slices.Concat(slices.Repeat([]int64{100}, 300), []int64{8 << 20}, slices.Repeat([]int64{100}, 300)), 2},
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

// A read of a session of few heavy records among light ones decodes the same entries, in the same order, at every width: the split is a CPU
// choice (D112) and Pi's single-threaded read is the reference, so the result of the read never depends on GOMAXPROCS.
func TestParallelHistoryReadIsIdenticalAtEveryWidth(t *testing.T) {
	dir := t.TempDir()
	session, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	var kept string
	for i := range 90 {
		// Mostly light records, a tool output of tens of kilobytes every ninth and of a megabyte every thirtieth.
		body := strings.Repeat("x", 40)
		switch {
		case i%30 == 29:
			body = strings.Repeat("heavy ", 1<<20/6)
		case i%9 == 8:
			body = strings.Repeat("output ", 45<<10/7)
		}
		id, err := session.AppendMessage(mkUserMsg(fmt.Sprintf("%03d %s", i, body)))
		if err != nil {
			t.Fatal(err)
		}
		if i%10 == 8 {
			kept = id
		}
		if i%10 == 9 {
			if _, err := session.AppendCompaction("summary", kept, 1000, nil, false, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := session.Path()
	whole := residentCopy(t, path)
	for _, procs := range []int{1, 2, 3, 4, 16} {
		t.Run(fmt.Sprintf("procs=%d", procs), func(t *testing.T) {
			defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(procs))
			indexed, _, err := loadPersistedSession(path)
			if err != nil {
				t.Fatal(err)
			}
			evicted := 0
			for i := range indexed.slots {
				if indexed.resident.get(i) == nil {
					evicted++
				}
			}
			if evicted == 0 {
				t.Fatal("no entry evicted: the read would decode nothing")
			}
			sameEntries(t, "entries", indexed.GetEntries(), whole.GetEntries())
			sameEntries(t, "branch", indexed.GetBranch(), whole.GetBranch())
			if got := indexed.staleReads.Load(); got != 0 {
				t.Fatalf("%d stale reads", got)
			}
		})
	}
}
