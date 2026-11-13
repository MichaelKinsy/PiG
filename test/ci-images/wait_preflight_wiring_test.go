// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// pull_request_target runs preflight.yml from the default branch whatever a pull request's base, so the Pre-flight verdict job must ask
// wait-preflight.sh about the default branch. Passing base.sha makes a pull request against a branch that has the file while main
// does not wait the full 15 minutes for a status no workflow will record.
func TestPreflightVerdictJobPassesTheDefaultBranchNotTheBase(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string
				Env map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if !strings.Contains(step.Run, "wait-preflight.sh") {
				continue
			}
			found++
			if want := `automation/ci/wait-preflight.sh "$REPO" "$HEAD_SHA" "$DEFAULT_BRANCH"`; strings.TrimSpace(step.Run) != want {
				t.Errorf("run = %q, want %q", step.Run, want)
			}
			if got, want := step.Env["DEFAULT_BRANCH"], "${{ github.event.repository.default_branch }}"; got != want {
				t.Errorf("env DEFAULT_BRANCH = %q, want %q", got, want)
			}
			for name, value := range step.Env {
				if strings.Contains(value, "base.sha") {
					t.Errorf("env %s = %q: the pull request's base says nothing about where preflight.yml runs from", name, value)
				}
			}
		}
	}
	if found != 1 {
		t.Errorf("%d steps run wait-preflight.sh, want 1", found)
	}
}
