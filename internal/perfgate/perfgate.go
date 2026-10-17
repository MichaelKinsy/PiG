// SPDX-License-Identifier: MIT

// Package perfgate checks count metrics against the ratchet ceilings in automation/perf/ratchet.toml.
//
// A gate test measures a count that does not depend on machine speed (allocations, bytes, GC cycles, goroutines, descriptors, child processes, crossings, file sizes) and calls [Check] with the metric's ID. Check fails when the count exceeds the ceiling or the ID has no ceiling, and logs one `perfgate: <id>=<value> ceiling=<ceiling>` line that `make perf-scoreboard-measure` collects. Ceilings only move down: automation/perf/scoreboard.py --ratchet-base rejects a raised ceiling. Only test code imports this package.
package perfgate

import (
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// RatchetPath is the ceiling file, relative to the module root.
const RatchetPath = "automation/perf/ratchet.toml"

// Ratchet is the decoded ceiling file.
type Ratchet struct {
	Ceilings map[string]float64 `toml:"ceilings"`
}

var loaded struct {
	once    sync.Once
	ratchet Ratchet
	err     error
}

// Load decodes the ratchet file at root.
func Load(root string) (Ratchet, error) {
	var ratchet Ratchet
	if _, err := toml.DecodeFile(filepath.Join(root, RatchetPath), &ratchet); err != nil {
		return Ratchet{}, fmt.Errorf("perfgate: %s: %w", RatchetPath, err)
	}
	for id, ceiling := range ratchet.Ceilings {
		if ceiling < 0 || math.IsNaN(ceiling) || math.IsInf(ceiling, 0) {
			return Ratchet{}, fmt.Errorf("perfgate: %s: ceiling %s = %v is not a finite count", RatchetPath, id, ceiling)
		}
	}
	return ratchet, nil
}

// Ceiling returns the ceiling of id, failing t when the ratchet file cannot be read or has no entry for id.
func Ceiling(t testing.TB, id string) float64 {
	t.Helper()
	loaded.once.Do(func() { loaded.ratchet, loaded.err = Load(testenv.ModuleRoot(t)) })
	if loaded.err != nil {
		t.Fatal(loaded.err)
	}
	ceiling, ok := loaded.ratchet.Ceilings[id]
	if !ok {
		t.Fatalf("perfgate: %s has no ceiling in %s", id, RatchetPath)
	}
	return ceiling
}

// Check fails t when got exceeds the ceiling of id.
func Check(t testing.TB, id string, got float64) {
	t.Helper()
	ceiling := Ceiling(t, id)
	t.Logf("perfgate: %s=%s ceiling=%s", id, format(got), format(ceiling))
	if got > ceiling {
		t.Errorf("perfgate: %s = %s, ceiling %s (%s). Find the new work with a profile; raise a ceiling only with the profile and the owner's approval", id, format(got), format(ceiling), RatchetPath)
	}
}

func format(value float64) string {
	if value == math.Trunc(value) && math.Abs(value) < 1e15 {
		return fmt.Sprintf("%d", int64(value))
	}
	return fmt.Sprintf("%.2f", value)
}
