// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type verdictWorkflow struct {
	Concurrency struct {
		Group            string
		CancelInProgress bool `yaml:"cancel-in-progress"`
	}
	Jobs map[string]struct {
		TimeoutMinutes int `yaml:"timeout-minutes"`
		Permissions    map[string]string
		Steps          []struct {
			Run  string
			Env  map[string]string
			With map[string]string
		}
	}
}

func readCIWorkflow(t *testing.T) verdictWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow verdictWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

// pull_request_target runs preflight.yml from the default branch whatever a pull request's base, so the Pre-flight verdict job must ask
// wait-preflight.py about the default branch. Passing base.sha makes a pull request against a branch that has the file while main
// does not wait for a status no workflow will record.
func TestPreflightVerdictJobPassesTheDefaultBranchNotTheBase(t *testing.T) {
	found := 0
	for _, job := range readCIWorkflow(t).Jobs {
		for _, step := range job.Steps {
			if !strings.Contains(step.Run, "wait-preflight.py") {
				continue
			}
			found++
			if want := `python3 automation/ci/wait-preflight.py "$REPO" "$HEAD_SHA" "$DEFAULT_BRANCH"`; strings.TrimSpace(step.Run) != want {
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
		t.Errorf("%d steps run wait-preflight.py, want 1", found)
	}
}

// The verdict job follows the state of the pre-flight run (wait-preflight.py), so a slow runner pool does not fail a pull request; its own
// timeout is the only clock. It holds one ubuntu-24.04 slot per minute waited, a slot the pre-flight itself needs, so the bound stays at
// the pre-flight's worst observed run with margin (60 minutes). It needs the run list (actions: read) and the status (statuses: read) and
// nothing that writes, and a new push cancels it through the workflow's concurrency group.
func TestPreflightVerdictJobFollowsTheRunWithinItsBound(t *testing.T) {
	workflow := readCIWorkflow(t)
	job, ok := workflow.Jobs["verdict"]
	if !ok {
		t.Fatal("ci.yml has no verdict job")
	}
	if job.TimeoutMinutes != 60 {
		t.Errorf("timeout-minutes = %d, want 60: the only clock of the wait", job.TimeoutMinutes)
	}
	want := map[string]string{"contents": "read", "actions": "read", "statuses": "read"}
	if len(job.Permissions) != len(want) {
		t.Errorf("permissions = %v, want %v", job.Permissions, want)
	}
	for scope, level := range want {
		if job.Permissions[scope] != level {
			t.Errorf("permission %s = %q, want %q", scope, job.Permissions[scope], level)
		}
	}
	if len(job.Steps) == 0 || job.Steps[0].With["sparse-checkout"] != "automation/ci/wait-preflight.py" {
		t.Errorf("the verdict job checks out %v, want automation/ci/wait-preflight.py alone", job.Steps)
	}
	if !strings.Contains(workflow.Concurrency.Group, "github.ref") || !workflow.Concurrency.CancelInProgress {
		t.Errorf("concurrency = %+v: a new push must cancel the waiting verdict job of the old head", workflow.Concurrency)
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
		if !strings.Contains(step.Run, "wait-preflight.py") {
			continue
		}
		waits++
		if got, want := step.If, "github.event_name == 'pull_request'"; got != want {
			t.Errorf("the wait step's if = %q, want %q: only a pull request has a pre-flight status to wait for", got, want)
		}
	}
	if waits != 1 {
		t.Errorf("%d steps of the verdict job run wait-preflight.py, want 1", waits)
	}
}
