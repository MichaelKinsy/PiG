// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Wall-clock and speed-ratio gates warn in pull-request CI and block in the release perf run (owner ruling 2026-10-15): ci.yml sets
// PIG_PERF_TIMING_REPORT_ONLY=1 once, for the whole workflow, and make test-timing runs the listed gates with it removed.
func TestTimingGatesAreReportOnlyInCIAndBlockingInTheReleaseRun(t *testing.T) {
	root := repoRoot(t)
	workflow, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	topLevel := regexp.MustCompile(`(?m)^env:\n(?:  .*\n)*?  PIG_PERF_TIMING_REPORT_ONLY: '1'\n`)
	if !topLevel.Match(workflow) {
		t.Error("ci.yml must set PIG_PERF_TIMING_REPORT_ONLY: '1' in its workflow-level env")
	}
	if got := strings.Count(string(workflow), "PIG_PERF_TIMING_REPORT_ONLY"); got != 1 {
		t.Errorf("ci.yml names PIG_PERF_TIMING_REPORT_ONLY %d times, want the one workflow-level assignment and no job or step override", got)
	}
	runner, err := os.ReadFile(filepath.Join(root, "automation/perf/timing_gates.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runner), `if k != "PIG_PERF_TIMING_REPORT_ONLY"`) {
		t.Error("automation/perf/timing_gates.py must remove PIG_PERF_TIMING_REPORT_ONLY so the gates block")
	}
	prerequisites, _ := makeDatabase(t)
	if _, ok := prerequisites["test-timing"]; !ok {
		t.Error("the Makefile has no test-timing target")
	}
	if !strings.Contains(strings.Join(prerequisites["release-check"], " "), "test-timing") {
		t.Error("release-check must run test-timing")
	}
}
