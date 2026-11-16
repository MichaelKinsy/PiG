// SPDX-License-Identifier: MIT

// Package benchalloc is test support for the benchmarks whose allocation row follows the catalog: it reports their allocations per 1,000 catalog
// entries instead of per pass over the catalog, so that a catalog that gains entries does not move the row while a record that costs more does.
// Only _test.go files import it; automation/perf/linked-packages.toml forbids it in every binary.
package benchalloc

import (
	"runtime"
	"testing"
)

// Per1000Entries runs pass once for each iteration of b.Loop and reports the iteration's allocations per 1,000 entries as allocs/op.
//
// One pass converts, encodes or lists entries catalog entries. The benchmark's allocs/op is the number of heap allocations that the loop made
// (runtime.MemStats.Mallocs, the counter testing reads for the same figure), divided by the iterations and by entries, times 1,000. testing documents
// that ReportMetric replaces a metric the framework itself reports, so the allocbase tool reads this figure as the benchmark's allocs/op. The tool runs
// -benchtime=5x, where an op that is one entry would be the first five entries of the catalog and an integer row would have a floor of two allocations
// (17 % of an entry's cost). The caller names its measured sub-benchmark per-1000-entries, so that the baseline row says what its unit is.
//
// The setup that must stay out of the figure belongs before the call; entries is the count of entries one pass handles.
func Per1000Entries(b *testing.B, entries int, pass func()) {
	b.Helper()
	if entries <= 0 {
		b.Fatal("benchalloc: a pass over no entries has no per-entry figure")
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for b.Loop() {
		pass()
	}
	runtime.ReadMemStats(&after)
	b.ReportMetric(float64(after.Mallocs-before.Mallocs)*1000/float64(entries)/float64(b.N), "allocs/op")
}
