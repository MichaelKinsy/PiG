// SPDX-License-Identifier: MIT

package core

import (
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// publishWatches and publishViews sort the open watch IDs (from a map, so in any order) on every commit. The sort is O(W log W): an
// insertion sort made each commit cost O(W^2) with W watches open, even for a commit that changes no watched document.
func TestSortU32CostsLogLinearInTheWatchCount(t *testing.T) {
	ids := make([]uint32, 100_000)
	for i := range ids {
		ids[i] = uint32(len(ids) - i)
	}
	start := time.Now()
	sortU32(ids)
	if !slices.IsSorted(ids) {
		t.Fatal("not sorted")
	}
	d := time.Since(start)
	testenv.TimingGate(t, d > 500*time.Millisecond, "sorting %d watch IDs took %v: the sort is not log-linear", len(ids), d)
}
