package subprocess

import (
	"context"
	"testing"
	"time"
)

func TestReloadReport_CapturesPlacementAndCacheHits(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/reportpack/a", "report-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/reportpack/b", "report-b", "tool_b")
	configs := []ExtConfig{
		packedFactoryConfig("report-a", rootA, "example.com/reportpack/a", "ha"),
		packedFactoryConfig("report-b", rootB, "example.com/reportpack/b", "hb"),
	}
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("first reload: %v", err)
	}
	rep := h.LastReloadReport()
	if rep == nil {
		t.Fatal("LastReloadReport() returned nil after Reload")
	}
	if rep.Duration <= 0 {
		t.Errorf("expected positive reload duration, got %v", rep.Duration)
	}
	if len(rep.Cells) != 1 {
		t.Fatalf("expected 1 packed cell report, got %d (%+v)", len(rep.Cells), rep.Cells)
	}
	cell := rep.Cells[0]
	if cell.Strategy != CellStrategyPackedGo {
		t.Errorf("strategy = %q, want packed-go", cell.Strategy)
	}
	if len(cell.Extensions) != 2 {
		t.Errorf("expected 2 members, got %v", cell.Extensions)
	}
	if cell.Cached {
		t.Errorf("expected cold build on first reload, got cached=true")
	}
	if cell.BuildDuration <= 0 {
		t.Errorf("expected positive cold build duration, got %v", cell.BuildDuration)
	}
	if cell.Hash == "" {
		t.Errorf("expected packed cell hash to be populated")
	}

	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("second reload: %v", err)
	}
	rep2 := h.LastReloadReport()
	if rep2 == nil || len(rep2.Cells) != 1 {
		t.Fatalf("expected 1 cell in second report, got %+v", rep2)
	}
	if !rep2.Cells[0].Cached {
		t.Errorf("expected cache hit on second reload, got Cached=false")
	}
	if rep2.Cells[0].BuildDuration != 0 {
		t.Errorf("expected zero BuildDuration on cache hit, got %v", rep2.Cells[0].BuildDuration)
	}
}

func TestReloadReport_QuarantineFissionsAppearInReport(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/reportq/a", "rq-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/reportq/b", "rq-b", "tool_b")
	configs := []ExtConfig{
		packedFactoryConfig("rq-a", rootA, "example.com/reportq/a", "ha"),
		packedFactoryConfig("rq-b", rootB, "example.com/reportq/b", "hb"),
	}
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	packedKey := h.exts["rq-a"].packedCellKey
	h.mu.Unlock()
	// CNC-002: a single crash's quarantine is released on the very next
	// explicit Reload (the packed cell gets one more chance to repack), so
	// this report only stays fissioned if the cell key's circuit breaker has
	// actually tripped. Quarantine it MaxCrashes times, matching the same
	// budget every isolated extension's own Supervisor already enforces,
	// before the Reload this test asserts on.
	for range DefaultSupervisorConfig().MaxCrashes {
		h.quarantinePackedCell(packedKey, "boom")
	}

	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	rep := h.LastReloadReport()
	if rep == nil {
		t.Fatal("nil report")
	}
	var quarantined int
	for _, c := range rep.Cells {
		if c.Quarantined {
			quarantined++
		}
	}
	if quarantined == 0 {
		t.Errorf("expected at least one quarantined cell in report, got %+v", rep.Cells)
	}
}
