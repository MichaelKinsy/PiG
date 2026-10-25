// SPDX-License-Identifier: MIT

package ciimages

import (
	"strings"
	"testing"
)

// The nightly cli shards pick their tests with automation/ci/test-shard-pattern.sh, which needs bash 4 (it declares an associative array). macOS ships bash 3.2
// as /bin/bash, and the four macOS shards died with "declare: -A: invalid option" before running a test. ci.yml's macOS job installs a current bash for the same
// script; the nightly race job does the same, before the step that runs the shards.
func TestNightlyMacOSShardsRunUnderACurrentBash(t *testing.T) {
	job, ok := readWorkflow(t, "nightly.yml").Jobs["race"]
	if !ok {
		t.Fatal("nightly.yml has no race job")
	}
	bashStep, raceStep := -1, -1
	for i, step := range job.Steps {
		switch {
		case strings.Contains(step.Run, "brew install bash"):
			bashStep = i
			if !strings.Contains(step.If, "macOS") || !strings.Contains(step.If, "matrix.shard") {
				t.Errorf("the bash step runs `if: %s`, want it limited to the macOS shards", step.If)
			}
			if !strings.Contains(step.Run, `echo "$(brew --prefix bash)/bin" >> "$GITHUB_PATH"`) {
				t.Errorf("the bash step does not put the bash keg on PATH:\n%s", step.Run)
			}
			if strings.Contains(step.Run, "brew --prefix)/bin") || strings.Contains(step.Run, "/opt/homebrew/bin") {
				t.Error("the bash step puts the whole Homebrew bin directory on PATH, where the image's node and npm would shadow the pinned toolchain")
			}
		case strings.Contains(step.Run, "make test-race-nightly"):
			raceStep = i
		}
	}
	if bashStep < 0 {
		t.Fatal("the nightly race job installs no current bash, so the macOS cli shards run test-shard-pattern.sh under bash 3.2")
	}
	if raceStep < 0 || bashStep > raceStep {
		t.Errorf("the bash step (%d) must come before the step that runs the race shards (%d): GITHUB_PATH applies to later steps only", bashStep, raceStep)
	}
}
