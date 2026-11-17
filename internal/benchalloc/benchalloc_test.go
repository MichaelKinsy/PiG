package benchalloc

import (
	"testing"
)

var sink *[4]int

// The figure is allocations per 1,000 entries whatever the pass size, and it replaces the framework's allocs/op.
func TestPer1000EntriesReportsAllocationsPerThousandEntries(t *testing.T) {
	const perEntry = 3
	for _, entries := range []int{10, 40} {
		result := testing.Benchmark(func(b *testing.B) {
			Per1000Entries(b, entries, func() {
				for range entries * perEntry {
					sink = new([4]int)
				}
			})
		})
		if got, want := result.AllocsPerOp(), int64(perEntry*1000); got < want || got > want+want/50 {
			t.Errorf("%d entries per pass: allocs/op = %d, want %d (3 allocations per entry, per 1,000 entries)", entries, got, want)
		}
	}
}

// A catalog that gains entries leaves the figure where it was: it moves with the cost of an entry only.
func TestPer1000EntriesFollowsTheCostOfAnEntry(t *testing.T) {
	figure := func(entries, perEntry int) int64 {
		return testing.Benchmark(func(b *testing.B) {
			Per1000Entries(b, entries, func() {
				for range entries * perEntry {
					sink = new([4]int)
				}
			})
		}).AllocsPerOp()
	}
	base, more, costlier := figure(100, 5), figure(150, 5), figure(100, 6)
	if more < base-base/50 || more > base+base/50 {
		t.Errorf("50 more entries moved the figure from %d to %d", base, more)
	}
	if costlier < base+base/10 {
		t.Errorf("one more allocation per entry moved the figure from %d to %d, want about %d", base, costlier, base+1000)
	}
}
