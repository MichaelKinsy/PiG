// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build durablescaling

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/core/driver"
)

// TestWarmTurnScaling measures the bench workload's warm turn at history sizes 10x apart: allocations, bytes and wall time per
// turn, excluding the first turn after open (which loads the history). Fixtures are seeded with seedFixture into
// DURABLE_SCALING_DIR and reused; DURABLE_SCALING_SIZES lists the sizes (default 50:500).
//
//	DURABLE_SCALING_DIR=<dir> DURABLE_SCALING_SIZES=500:5000 env -u GOMAXPROCS go test -tags durablescaling \
//	    ./durable/core/sqlhost/cmd/bench -run TestWarmTurnScaling -v -timeout 50m
func TestWarmTurnScaling(t *testing.T) {
	dir := os.Getenv("DURABLE_SCALING_DIR")
	if dir == "" {
		t.Fatal("DURABLE_SCALING_DIR names the fixture directory")
	}
	sizes := []int{50, 500}
	if s := os.Getenv("DURABLE_SCALING_SIZES"); s != "" {
		sizes = nil
		for _, f := range filepath.SplitList(s) {
			n, _ := strconv.Atoi(f)
			sizes = append(sizes, n)
		}
	}
	ctx := context.Background()
	for _, size := range sizes {
		fixture := filepath.Join(dir, fmt.Sprintf("pig-native-%d.sqlite", size))
		if _, err := os.Stat(fixture); err != nil {
			start := time.Now()
			if err := seedFixture(ctx, dir, size, "pig-native"); err != nil {
				t.Fatal(err)
			}
			t.Logf("seeded %d in %s", size, time.Since(start))
		}
		measure := func(turns int) (uint64, uint64, time.Duration) {
			work := filepath.Join(t.TempDir(), "store.sqlite")
			if err := copyFile(fixture, work); err != nil {
				t.Fatal(err)
			}
			o := k8sOptions(work, func(ctx context.Context, s *driver.Session, _ *script, _ measured) error { return s.Close(ctx) })
			o.turns = turns
			runtime.GC()
			var a, b runtime.MemStats
			runtime.ReadMemStats(&a)
			start := time.Now()
			if err := run(ctx, o); err != nil {
				t.Fatal(err)
			}
			el := time.Since(start)
			runtime.ReadMemStats(&b)
			return b.Mallocs - a.Mallocs, b.TotalAlloc - a.TotalAlloc, el
		}
		m0, b0, t0 := measure(1)
		m1, b1, t1 := measure(11)
		t.Logf("H=%d open: allocs %d bytes %d time %s; warm turn: allocs %d bytes %d time %s", size, m0, b0, t0, (m1-m0)/10, (b1-b0)/10, (t1-t0)/10)
	}
}
