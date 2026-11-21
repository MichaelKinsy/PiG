// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build !race

package runtimecell

import (
	"context"
	"testing"
	"time"
)

// The lookups of a load of 13 Rust cells read the SDK tree once, so they allocate what one lookup's SDK check costs 12 times less than lookups
// that check it for each cell (BenchmarkRustCellsOfOneLoad: 20204 allocations per-cell, 13744 per-load, and half the time). Pinning the tree memo
// clock to the fixture's write time keeps sdkTreeHash on the same unmemoized reading path across all 20 runs on slow platforms (Windows) where
// wall-clock elapsed time otherwise crosses racyTreeWindow mid-loop and switches sdkTreeHash to the cheaper memo-hit walk.
func TestRustCellsOfOneLoadAllocateTheSDKReadingOnce(t *testing.T) {
	emptyTreeMemo(t)
	cacheRoot, sdkRoot, cells := publishedRustCells(t, 13)
	pinTreeClock(t, time.Now())
	reading := testing.AllocsPerRun(20, func() { _ = sdkTreeHash(sdkRoot) })
	perCell := testing.AllocsPerRun(20, func() { lookUpRustCells(t, context.Background(), cacheRoot, cells) })
	perLoad := testing.AllocsPerRun(20, func() { lookUpRustCells(t, WithLoadTreeHashes(context.Background()), cacheRoot, cells) })
	if saved := perCell - perLoad; saved < 11*reading {
		t.Errorf("a load of 13 cells allocated %v when it read the SDK tree once, %v when it read it for each cell: saved %v, want at least 11 readings of %v", perLoad, perCell, saved, reading)
	}
}
