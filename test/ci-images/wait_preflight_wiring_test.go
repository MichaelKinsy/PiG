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

// Every event but a pull request runs ci.yml's full plan (CI result requires it), and a skipped job skips every job that
// needs it, directly or through another job: Bookkeeping's !cancelled() let it run after a skipped Pre-flight verdict,
// but the Linux, Windows and macOS jobs that need Bookkeeping were skipped with the verdict, so every nightly, release and
// dispatch run tested nothing and failed. A job that another job needs therefore never depends on the event; the verdict
// job runs everywhere and only waits for the pre-flight on a pull request, where preflight.yml records a status.
func TestANeededJobOfCIRunsOnEveryEvent(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			If    string
			Needs yaml.Node
			Steps []struct {
				If  string
				Run string
			}
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	needed := map[string]bool{}
	for name, job := range workflow.Jobs {
		var needs []string
		switch job.Needs.Kind {
		case 0:
		case yaml.ScalarNode:
			needs = []string{job.Needs.Value}
		default:
			if err := job.Needs.Decode(&needs); err != nil {
				t.Fatalf("job %s: needs: %v", name, err)
			}
		}
		for _, need := range needs {
			needed[need] = true
		}
	}
	if !needed["verdict"] {
		t.Fatal("no job needs the verdict job; the test no longer covers it")
	}
	for name := range needed {
		if job := workflow.Jobs[name]; strings.Contains(job.If, "github.event_name") {
			t.Errorf("job %s, which other jobs need, has if: %s; outside that event it is skipped and so is every job after it", name, job.If)
		}
	}
	waits := 0
	for _, step := range workflow.Jobs["verdict"].Steps {
		if !strings.Contains(step.Run, "wait-preflight.sh") {
			continue
		}
		waits++
		if got, want := step.If, "github.event_name == 'pull_request'"; got != want {
			t.Errorf("the wait step's if = %q, want %q: only a pull request has a pre-flight status to wait for", got, want)
		}
	}
	if waits != 1 {
		t.Errorf("%d steps of the verdict job run wait-preflight.sh, want 1", waits)
	}
}
