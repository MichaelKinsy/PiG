// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCIDAGShadowAndHermeticCacheWiring verifies that .github/workflows/ci.yml
// wires the fail-closed DAG selector (defaulting to shadow mode on product PR
// and merge_group runs, overridable via vars.PIG_CI_DAG) and the opt-in
// hermetic pure-Go test-cache partition on pull_request test-fast runs
// (overridable via vars.PIG_GO_HERMETIC_TEST_CACHE), without expanding token
// permissions or saving caches on pull requests.
func TestCIDAGShadowAndHermeticCacheWiring(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				ID   string            `yaml:"id"`
				Name string            `yaml:"name"`
				If   string            `yaml:"if"`
				Uses string            `yaml:"uses"`
				Env  map[string]string `yaml:"env"`
				With map[string]any    `yaml:"with"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}

	changes, ok := wf.Jobs["changes"]
	if !ok {
		t.Fatal("ci.yml has no changes job")
	}
	if changes.Permissions["contents"] != "read" || changes.Permissions["actions"] != "read" || len(changes.Permissions) != 2 {
		t.Errorf("changes job permissions = %v, want read-only contents+actions", changes.Permissions)
	}

	var setupGoIdx, restoreModIdx, planIdx, uploadIdx = -1, -1, -1, -1
	for i, step := range changes.Steps {
		switch {
		case strings.HasPrefix(step.Uses, "actions/setup-go@"):
			setupGoIdx = i
			if step.With["go-version-file"] != ".go-version" {
				t.Errorf("changes setup-go go-version-file = %v, want .go-version", step.With["go-version-file"])
			}
			if cache, _ := step.With["cache"].(bool); cache {
				t.Errorf("changes setup-go must set cache: false")
			}
			if !strings.Contains(step.If, "vars.PIG_CI_DAG") || !strings.Contains(step.If, "'shadow'") {
				t.Errorf("changes setup-go if = %q, want vars.PIG_CI_DAG defaulting to shadow", step.If)
			}
		case strings.HasPrefix(step.Uses, "actions/cache/restore@"):
			restoreModIdx = i
			if step.With["path"] != "~/go/pkg/mod" {
				t.Errorf("changes cache/restore path = %v, want ~/go/pkg/mod", step.With["path"])
			}
		case step.ID == "plan":
			planIdx = i
			if !strings.Contains(step.Env["PIG_CI_DAG"], "vars.PIG_CI_DAG") || !strings.Contains(step.Env["PIG_CI_DAG"], "'shadow'") {
				t.Errorf("plan PIG_CI_DAG = %q, want vars.PIG_CI_DAG defaulting to shadow", step.Env["PIG_CI_DAG"])
			}
			if !strings.Contains(step.Env["PIG_CI_DAG_BASE"], "github.event.pull_request.base.sha") {
				t.Errorf("plan PIG_CI_DAG_BASE = %q, want PR/merge_group base sha", step.Env["PIG_CI_DAG_BASE"])
			}
			if !strings.Contains(step.Env["PIG_CI_DAG_HEAD"], "github.event.pull_request.head.sha") {
				t.Errorf("plan PIG_CI_DAG_HEAD = %q, want PR/merge_group head sha", step.Env["PIG_CI_DAG_HEAD"])
			}
			if step.Env["PIG_CI_DAG_PLAN_OUT"] != "build/ci-dag-plan.json" {
				t.Errorf("plan PIG_CI_DAG_PLAN_OUT = %q, want build/ci-dag-plan.json", step.Env["PIG_CI_DAG_PLAN_OUT"])
			}
		case strings.HasPrefix(step.Uses, "actions/upload-artifact@"):
			uploadIdx = i
			if step.With["name"] != "ci-dag-plan" || step.With["path"] != "build/ci-dag-plan.json" {
				t.Errorf("upload-artifact with = %v, want name=ci-dag-plan path=build/ci-dag-plan.json", step.With)
			}
		}
	}
	if setupGoIdx < 0 || restoreModIdx < 0 || planIdx < 0 || uploadIdx < 0 {
		t.Fatalf("missing changes DAG steps: setupGo=%d restoreMod=%d plan=%d upload=%d", setupGoIdx, restoreModIdx, planIdx, uploadIdx)
	}
	if setupGoIdx >= restoreModIdx || restoreModIdx >= planIdx || planIdx >= uploadIdx {
		t.Errorf("changes DAG steps out of order: setupGo=%d restoreMod=%d plan=%d upload=%d", setupGoIdx, restoreModIdx, planIdx, uploadIdx)
	}

	linux, ok := wf.Jobs["linux"]
	if !ok {
		t.Fatal("ci.yml has no linux job")
	}
	foundHermetic := false
	for _, step := range linux.Steps {
		if step.Name == "Run PiG verification shard" {
			got := step.Env["PIG_GO_HERMETIC_TEST_CACHE"]
			if !strings.Contains(got, "matrix.shard == 'test-fast'") || !strings.Contains(got, "vars.PIG_GO_HERMETIC_TEST_CACHE") {
				t.Errorf("linux Run PiG verification shard PIG_GO_HERMETIC_TEST_CACHE = %q, want test-fast gated by vars.PIG_GO_HERMETIC_TEST_CACHE", got)
			}
			foundHermetic = true
		}
	}
	if !foundHermetic {
		t.Error("linux job has no Run PiG verification shard step")
	}
}
