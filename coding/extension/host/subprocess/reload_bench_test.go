package subprocess

import (
	"context"
	"testing"
	"time"
)

// BenchmarkReload_PackedCellWarmCache measures the wall time of a Reload()
// invocation where the packed Go artifact is already cached. This is the
// production-hot path: most reloads should hit cache and complete in single
// digit milliseconds beyond subprocess respawn cost.
//
// Run with:
//
//	go test ./coding/extension/host/subprocess -run=^$ -bench=BenchmarkReload_PackedCellWarmCache -benchtime=5x
func BenchmarkReload_PackedCellWarmCache(b *testing.B) {
	rootA := writePackedFactoryModule(b, "example.com/bench/a", "bench-a", "tool_a")
	rootB := writePackedFactoryModule(b, "example.com/bench/b", "bench-b", "tool_b")
	configs := []ExtConfig{
		packedFactoryConfig("bench-a", rootA, "example.com/bench/a", "ha"),
		packedFactoryConfig("bench-b", rootB, "example.com/bench/b", "hb"),
	}
	h := NewHost(b.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	b.Cleanup(func() { h.Shutdown("bench done") })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	// Cold build once outside the timed loop so subsequent Reload() invocations
	// hit the packed-cell cache.
	if _, err := h.Reload(ctx); err != nil {
		b.Fatalf("warmup reload: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.Reload(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPlanCells_Isolated measures planner throughput for a realistic
// extension count when every extension runs in an isolated process.
// PlanCells must stay near-zero-cost: it runs on every Reload() and on every
// validation, and it is not parallel.
func BenchmarkPlanCells_Isolated(b *testing.B) {
	configs := make([]ExtConfig, 0, 24)
	for i := range 24 {
		configs = append(configs, ExtConfig{
			Name:        cellNameForBench(i),
			Enabled:     true,
			Source:      "/tmp/example",
			RuntimeKind: "subprocess",
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = PlanCells(configs, nil)
	}
}

func cellNameForBench(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	return "ext-" + string(letters[i%len(letters)]) + "-" + string(letters[(i/26)%len(letters)])
}
