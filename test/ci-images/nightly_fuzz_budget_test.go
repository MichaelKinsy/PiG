// SPDX-License-Identifier: MIT

package ciimages

import (
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
	need := fuzzSetup + time.Duration(targets)*(fuzztime+fuzzTargetOverhead)
	if have := time.Duration(job.TimeoutMinutes) * time.Minute; have < need {
		t.Errorf("the fuzz job's timeout is %v, but %d targets at %v plus %v of build each need %v; raise timeout-minutes of the job (or split the job)", have, targets, fuzztime, fuzzTargetOverhead, need)
	}
}
