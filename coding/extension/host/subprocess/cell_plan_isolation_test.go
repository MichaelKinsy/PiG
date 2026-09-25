package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// Bug 1 (D20 conformance): packability is gated positively on shared-ok, so an
// unrecognized isolation token must fail safe to an isolated cell instead of
// being silently packed. "dedicated" is the token that triggered the original
// pig-mcp-adapter build failure.
func TestPlanCellsRoutesUnknownIsolationToIsolated(t *testing.T) {
	cfg := packableConfig("unknown-iso", "hu")
	cfg.Isolation = "dedicated"
	cells := PlanCells([]ExtConfig{cfg}, nil)
	if len(cells) != 1 {
		t.Fatalf("want 1 cell, got %d: %+v", len(cells), cells)
	}
	if cells[0].Strategy != CellStrategyIsolated {
		t.Fatalf("unknown isolation %q must route isolated, got %s", cfg.Isolation, cells[0].Strategy)
	}
}

// Empty isolation is the default and means shared-ok, so it must still pack.
func TestPlanCellsPacksDefaultIsolation(t *testing.T) {
	cfg := packableConfig("default-iso", "hd")
	cfg.Isolation = ""
	cells := PlanCells([]ExtConfig{cfg}, nil)
	if len(cells) != 1 || cells[0].Strategy != CellStrategyPackedGo {
		t.Fatalf("empty isolation should pack (default shared-ok), got %+v", cells)
	}
}

func TestPlanCellsPacksMultiPackageGoFactory(t *testing.T) {
	dir := t.TempDir()
	writeExtFile(t, filepath.Join(dir, "main.go"), "package main\n")
	writeExtFile(t, filepath.Join(dir, "internal", "auth", "auth.go"), "package auth\n")
	cfg := packableConfig("multi-pkg", "hm")
	cfg.Source = dir
	cells := PlanCells([]ExtConfig{cfg}, nil)
	if len(cells) != 1 || cells[0].Strategy != CellStrategyPackedGo {
		t.Fatalf("multi-package go ext should pack through its importable factory, got %+v", cells)
	}
}

// A flat single-package extension still packs; Go-ignored dirs (testdata) must
// not trip the sub-package detector.
func TestPlanCellsPacksFlatSinglePackageGo(t *testing.T) {
	dir := t.TempDir()
	writeExtFile(t, filepath.Join(dir, "main.go"), "package main\n")
	writeExtFile(t, filepath.Join(dir, "helpers.go"), "package main\n")
	writeExtFile(t, filepath.Join(dir, "testdata", "x.go"), "package x\n")
	cfg := packableConfig("flat", "hflat")
	cfg.Source = dir
	cells := PlanCells([]ExtConfig{cfg}, nil)
	if len(cells) != 1 || cells[0].Strategy != CellStrategyPackedGo {
		t.Fatalf("flat single-package go ext should pack, got %+v", cells)
	}
}

func writeExtFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
