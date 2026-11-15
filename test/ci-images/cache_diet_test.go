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
// limit and evicted main's. Every cache is saved only by the nightly scheduled run on main, a dispatch there, and a dispatch on a release branch: ci.yml and
// security.yml restore with actions/cache/restore and save with actions/cache/save under CACHE_SAVE, which no pull request, merge queue or push run satisfies.
func TestBigCachesSaveOnlyFromMainAndReleaseDispatch(t *testing.T) {
	const want = "${{ (github.ref == 'refs/heads/main' && (github.event_name == 'schedule' || github.event_name == 'workflow_dispatch')) || (github.event_name == 'workflow_dispatch' && startsWith(github.ref, 'refs/heads/release/')) }}"
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
					// Every cache, not only the big ones: a small exact-key cache a pull request saves is still one copy per pull request.
					t.Errorf("%s job %s: step %q (key %q) restores and saves with actions/cache; use actions/cache/restore and a save step under env.CACHE_SAVE", name, jobName, step.Name, key)
				case strings.HasPrefix(step.Uses, "actions/setup-go@"):
					if cache, _ := step.With["cache"].(bool); cache {
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

// The weekly CI run became the nightly one, because it is the only scheduled run that saves caches.
func TestCIRunsNightlyOnMainAndSavesCachesOnlyFromIt(t *testing.T) {
	workflow := readCacheDietWorkflow(t, "ci.yml")
	schedule, _ := workflow.On["schedule"].([]any)
	if len(schedule) != 1 {
		t.Fatalf("ci.yml schedule = %v, want one cron", workflow.On["schedule"])
	}
	cron, _ := schedule[0].(map[string]any)["cron"].(string)
	if fields := strings.Fields(cron); len(fields) != 5 || fields[2] != "*" || fields[3] != "*" || fields[4] != "*" {
		t.Errorf("ci.yml cron = %q, want a nightly one (every day of the week)", cron)
	}
}

// A lockfile change is the one case where a whole cache goes invalid, so cache-warm.yml runs on a push to main only for one.
func TestCacheWarmRunsOnlyForALockfileChange(t *testing.T) {
	workflow := readCacheDietWorkflow(t, "cache-warm.yml")
	if _, ok := workflow.On["schedule"]; ok {
		t.Error("cache-warm.yml has a schedule; the nightly CI run saves the caches")
	}
	push, _ := workflow.On["push"].(map[string]any)
	paths, _ := push["paths"].([]any)
	if len(paths) == 0 {
		t.Error("cache-warm.yml runs on every push to main; limit it to the lockfiles")
	}
	for _, p := range paths {
		if !strings.HasSuffix(p.(string), "go.sum") && !strings.HasSuffix(p.(string), "Cargo.lock") && !strings.HasSuffix(p.(string), "package-lock.json") {
			t.Errorf("cache-warm.yml push path %q is not a lockfile", p)
		}
	}
}

// The eight portmap shards' keys share the prefix portmap-coverage-, so pruning by it kept one shard's ledger and deleted the other seven.
func TestCachePruneKeepsOnePortmapLedgerPerShard(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "gocache-macOS- golangci-lint-Linux- pi-oracle-Linux- pi-oracle-Windows- pi-oracle-macOS-; do") == false {
		t.Error("the prune loop's prefix list changed; update this test")
	}
	if strings.Contains(text, "Windows- portmap-coverage- ") {
		t.Error("cache-prune groups every portmap shard under portmap-coverage-, so it keeps one shard's ledger")
	}
	if !strings.Contains(text, `sub("-[0-9a-f]{40}$"; "")`) {
		t.Error("cache-prune does not group the portmap ledgers by shard (the key without its commit)")
	}
}
