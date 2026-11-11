// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type cacheDietWorkflow struct {
	On          map[string]any
	Env         map[string]string
	Permissions any
	Concurrency struct {
		CancelInProgress any `yaml:"cancel-in-progress"`
	}
	Jobs map[string]struct {
		If          string
		Permissions map[string]string
		Steps       []struct {
			Name string
			If   string
			Uses string
			Run  string
			With map[string]any
		}
	}
}

func readCacheDietWorkflow(t *testing.T, name string) cacheDietWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	var workflow cacheDietWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return workflow
}

// A pull request's caches are visible to that pull request alone, so a copy of the Go build cache or the Cargo targets per pull request filled the repository's cache
// limit and evicted main's. The big caches are saved from main and from a release branch's dispatch only: ci.yml and security.yml restore them with actions/cache/restore
// and save them with actions/cache/save under CACHE_SAVE, which no pull request run or merge queue run satisfies.
func TestBigCachesSaveOnlyFromMainAndReleaseDispatch(t *testing.T) {
	const want = "${{ github.event_name != 'pull_request' && (github.ref == 'refs/heads/main' || (github.event_name == 'workflow_dispatch' && startsWith(github.ref, 'refs/heads/release/'))) }}"
	big := []string{"gocache-", "gomod-", "cargo-", "node-cache-", "durable-cf-host-"}
	// The macOS jobs keep setup-go's cache: one entry per go.sum that cache-warm.yml saves from main, not one per commit or pull request.
	setupGoCache := map[string]bool{"ci.yml/macos": true}
	for _, name := range []string{"ci.yml", "security.yml"} {
		workflow := readCacheDietWorkflow(t, name)
		if got := workflow.Env["CACHE_SAVE"]; got != want {
			t.Errorf("%s: env CACHE_SAVE = %q, want %q", name, got, want)
		}
		saves := 0
		for jobName, job := range workflow.Jobs {
			for _, step := range job.Steps {
				key, _ := step.With["key"].(string)
				switch {
				case strings.HasPrefix(step.Uses, "actions/cache/save@"):
					saves++
					if !strings.Contains(step.If, "env.CACHE_SAVE == 'true'") {
						t.Errorf("%s job %s: save step %q is not conditioned on env.CACHE_SAVE (if: %q)", name, jobName, step.Name, step.If)
					}
				case strings.HasPrefix(step.Uses, "actions/cache@"):
					for _, prefix := range big {
						if strings.HasPrefix(key, prefix) {
							t.Errorf("%s job %s: step %q restores and saves the %s cache with actions/cache; use actions/cache/restore and a save step under env.CACHE_SAVE", name, jobName, step.Name, prefix)
						}
					}
				case strings.HasPrefix(step.Uses, "actions/setup-go@"):
					if cache, _ := step.With["cache"].(bool); cache && !setupGoCache[name+"/"+jobName] {
						t.Errorf("%s job %s: setup-go caches on every run, including a pull request's; restore gomod- and gocache- explicitly", name, jobName)
					}
				case strings.HasPrefix(step.Uses, "actions/setup-node@"):
					if step.With["cache"] != nil {
						t.Errorf("%s job %s: setup-node's cache saves from a pull request; restore node-cache- explicitly", name, jobName)
					}
				}
				if step.Name == "Keep only the Go build cache entries this job used" && !strings.Contains(step.If, "env.CACHE_SAVE == 'true'") {
					t.Errorf("%s job %s: the build cache trim runs without a save to follow it", name, jobName)
				}
			}
		}
		if saves == 0 {
			t.Errorf("%s has no actions/cache/save step; the pattern in this test is stale", name)
		}
	}
}

// A pull request's caches are visible to that pull request alone, so they are dead weight once it closes: this workflow deletes them.
func TestCacheCleanupDeletesAClosedPullRequestsCaches(t *testing.T) {
	workflow := readCacheDietWorkflow(t, "cache-cleanup.yml")
	if _, ok := workflow.On["pull_request"]; !ok || len(workflow.On) != 1 {
		t.Errorf("cache-cleanup.yml triggers = %v, want pull_request alone", workflow.On)
	}
	if perms, ok := workflow.Permissions.(map[string]any); !ok || len(perms) != 0 {
		t.Errorf("cache-cleanup.yml workflow permissions = %v, want none", workflow.Permissions)
	}
	job, ok := workflow.Jobs["cleanup"]
	if !ok {
		t.Fatal("cache-cleanup.yml has no cleanup job")
	}
	if len(job.Permissions) != 1 || job.Permissions["actions"] != "write" {
		t.Errorf("cleanup job permissions = %v, want actions: write alone", job.Permissions)
	}
	if !strings.Contains(job.If, "github.event.pull_request.head.repo.full_name == github.repository") {
		t.Errorf("cleanup job if = %q: a fork's read-only token cannot delete caches", job.If)
	}
	var run string
	for _, step := range job.Steps {
		if step.Uses != "" {
			t.Errorf("cleanup job step %q uses %s; the job needs only gh and checks out no pull request code", step.Name, step.Uses)
		}
		run += step.Run
	}
	for _, text := range []string{"gh cache list", "--ref \"$REF\"", "gh cache delete"} {
		if !strings.Contains(run, text) {
			t.Errorf("cleanup job does not run %s", text)
		}
	}
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "cache-cleanup.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "REF: refs/pull/${{ github.event.pull_request.number }}/merge") {
		t.Error("cache-cleanup.yml does not delete the caches of refs/pull/<N>/merge")
	}
	if !strings.Contains(string(data), "types: [closed]") {
		t.Error("cache-cleanup.yml does not run when a pull request closes")
	}
}

// A dispatch from main publishes the CI images and a cancelled publish leaves a half-pushed set, so only a pull request's run is cancelled by a newer push.
func TestCIImagesCancelsOnlySupersededPullRequestRuns(t *testing.T) {
	workflow := readCacheDietWorkflow(t, "ci-images.yml")
	if got := workflow.Concurrency.CancelInProgress; got != "${{ github.event_name == 'pull_request' }}" {
		t.Errorf("ci-images.yml cancel-in-progress = %v, want it true for a pull request only", got)
	}
}
