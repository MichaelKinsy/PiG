// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build !race

package runtimecell

import (
	"context"
	"testing"
)

// The lookups of a load of 13 Rust cells read the SDK tree once, so they allocate what a reading of the tree costs 12 times less than the lookups that
// read it for each cell (BenchmarkRustCellsOfOneLoad: 20204 allocations per-cell, 13744 per-load, and half the time).
func TestRustCellsOfOneLoadAllocateTheSDKReadingOnce(t *testing.T) {
	cacheRoot, sdkRoot, cells := publishedRustCells(t, 13)
	reading := testing.AllocsPerRun(20, func() { _ = hashTree(sdkRoot) })
	perCell := testing.AllocsPerRun(20, func() { lookUpRustCells(t, context.Background(), cacheRoot, cells) })
	perLoad := testing.AllocsPerRun(20, func() { lookUpRustCells(t, WithLoadTreeHashes(context.Background()), cacheRoot, cells) })
	if saved := perCell - perLoad; saved < 11*reading {
		t.Errorf("a load of 13 cells allocated %v when it read the SDK tree once, %v when it read it for each cell: saved %v, want at least 11 readings of %v", perLoad, perCell, saved, reading)
	}
}
