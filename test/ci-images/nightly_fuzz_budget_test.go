// SPDX-License-Identifier: MIT

package ciimages

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fuzzTarget is the declaration automation/ci/fuzz-nightly.sh lists targets by.
var fuzzTarget = regexp.MustCompile(`(?m)^func Fuzz[A-Za-z0-9_]+\([A-Za-z_][A-Za-z0-9_]* \*testing\.F\)`)

// fuzzTargetOverhead is what one target costs beyond its -fuzztime: go test builds the coverage-instrumented package, gathers the seed corpus's baseline coverage and minimizes
// before it exits. The nightly of 2026-11-08 ran 41 targets in 1695 s at 30 s each, 41 s a target.
const fuzzTargetOverhead = 15 * time.Second

// fuzzSetup covers the job's checkout, Go setup and first build.
const fuzzSetup = 5 * time.Minute

// make fuzz-nightly runs every target one after the other, so the fuzz job's timeout has to cover every target's time. The job had a 30 minute timeout, the targets of the module
// grew past what fits in it, and the nightly was cancelled at the 41st target on 2026-11-05, 11-06 and 11-08 with the rest never fuzzed.
func TestNightlyFuzzJobTimeoutCoversEveryTarget(t *testing.T) {
	job, ok := readWorkflow(t, "nightly.yml").Jobs["fuzz"]
	if !ok {
		t.Fatal("nightly.yml has no fuzz job")
	}
	fuzztime := time.Duration(0)
	for _, step := range job.Steps {
		if value, ok := step.Env["PIG_FUZZ_TIME"]; ok {
			d, err := time.ParseDuration(value)
			if err != nil {
				t.Fatalf("PIG_FUZZ_TIME %q: %v", value, err)
			}
			fuzztime = d
		}
	}
	if fuzztime == 0 {
		t.Fatal("no step of the nightly fuzz job sets PIG_FUZZ_TIME")
	}
	root := repoRoot(t)
	targets := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", ".upstream", ".cache":
				return filepath.SkipDir
			case "tmp":
				// Scratch that git ignores (`tmp/` in .gitignore), not the repository's code.
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		targets += len(fuzzTarget.FindAll(source, -1))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if targets == 0 {
		t.Fatal("found no Fuzz targets")
	}
	shards := 1
	if len(job.Strategy.Matrix.Shard) > 0 {
		shards = len(job.Strategy.Matrix.Shard)
		for i, spec := range job.Strategy.Matrix.Shard {
			want := fmt.Sprintf("%d/%d", i+1, shards)
			if spec != want {
				t.Errorf("fuzz matrix shard[%d] = %q, want %q", i, spec, want)
			}
		}
	}
	maxPerShard := (targets + shards - 1) / shards
	need := fuzzSetup + time.Duration(maxPerShard)*(fuzztime+fuzzTargetOverhead)
	if have := time.Duration(job.TimeoutMinutes) * time.Minute; have < need {
		t.Errorf("the fuzz job's timeout is %v, but %d targets across %d shards (up to %d per shard) at %v plus %v of build each need %v; raise timeout-minutes of the job (or split the job further)", have, targets, shards, maxPerShard, fuzztime, fuzzTargetOverhead, need)
	}
}

// TestNightlyRaceMacOSPackingCoversEveryShard verifies that the packed macOS nightly race matrix covers every single-package
// shard of cli (1..7/7), tui (1..8/8), and codingagent (1..12/12) exactly once while staying at or under 20 macOS jobs
// (4 waves at GitHub's 5-job macOS concurrency cap, down from 35 jobs / 7 waves).
func TestNightlyRaceMacOSPackingCoversEveryShard(t *testing.T) {
	job, ok := readWorkflow(t, "nightly.yml").Jobs["race"]
	if !ok {
		t.Fatal("nightly.yml has no race job")
	}
	macJobs := 0
	for _, osName := range job.Strategy.Matrix.OS {
		if osName == "macos-15" {
			macJobs += len(job.Strategy.Matrix.Group)
		}
	}
	wantShards := map[string]int{
		"cli":         7,
		"tui":         8,
		"codingagent": 12,
	}
	seen := map[string]map[string]int{
		"cli":         {},
		"tui":         {},
		"codingagent": {},
	}
	includes, _ := job.Strategy.Matrix.Include.([]any)
	for _, raw := range includes {
		row, _ := raw.(map[string]any)
		if fmt.Sprint(row["os"]) != "macos-15" {
			continue
		}
		macJobs++
		group := fmt.Sprint(row["group"])
		if _, sharded := wantShards[group]; !sharded {
			continue
		}
		for shard := range strings.FieldsSeq(fmt.Sprint(row["shard"])) {
			seen[group][shard]++
		}
	}
	if macJobs > 20 {
		t.Errorf("nightly macOS race matrix has %d jobs, want <= 20 (packed to <= 4 waves of 5)", macJobs)
	}
	for group, total := range wantShards {
		if len(seen[group]) != total {
			t.Errorf("macOS %s covers %d distinct shards (%v), want %d", group, len(seen[group]), seen[group], total)
		}
		for k := 1; k <= total; k++ {
			key := fmt.Sprintf("%d/%d", k, total)
			if seen[group][key] != 1 {
				t.Errorf("macOS %s shard %s appears %d times, want 1", group, key, seen[group][key])
			}
		}
	}
}
