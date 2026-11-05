// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// integrationBranches reads automation/ci/integration-branches.txt, the one list of long-lived integration branch names.
func integrationBranches(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "automation/ci/integration-branches.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			names = append(names, line)
		}
	}
	if len(names) == 0 {
		t.Fatal("automation/ci/integration-branches.txt names no branch")
	}
	for _, name := range names {
		if strings.ContainsAny(name, "*?[") {
			t.Errorf("%q is a pattern: the list holds exact names, so lane branches such as fix/follow-pi-<lane> do not match", name)
		}
	}
	return names
}

// The branch names live in automation/ci/integration-branches.txt. Two workflows read it at run time (the job order in ci.yml and the nightly dispatch in
// integration-ci.yml); a push filter cannot read a file, so ci.yml's must list exactly those names next to the release and integration patterns.
func TestIntegrationBranchesHaveOneSource(t *testing.T) {
	names := integrationBranches(t)
	push, ok := readWorkflow(t, "ci.yml").triggers()["push"].(map[string]any)
	if !ok {
		t.Fatal("ci.yml has no push trigger")
	}
	var filter []string
	for _, branch := range push["branches"].([]any) {
		filter = append(filter, branch.(string))
	}
	want := append([]string{"release/**", "integration/**"}, names...)
	slices.Sort(filter)
	slices.Sort(want)
	if !slices.Equal(filter, want) {
		t.Errorf("ci.yml push branches %v, want %v (release/**, integration/** and the names in automation/ci/integration-branches.txt)", filter, want)
	}

	nightly := readWorkflow(t, "integration-ci.yml")
	var reads bool
	for _, step := range nightly.Jobs["dispatch"].Steps {
		if strings.Contains(step.Run, "automation/ci/integration-branches.txt") {
			reads = true
		}
		if strings.Contains(step.Env["BRANCHES"], "fix/") {
			t.Errorf("integration-ci.yml names a branch in %q; it reads automation/ci/integration-branches.txt", step.Env["BRANCHES"])
		}
	}
	if !reads {
		t.Error("integration-ci.yml does not read automation/ci/integration-branches.txt")
	}
}

// The os-parallel step of ci.yml, run as written for a base or pushed branch: the listed names and release/* and integration/* are parallel, and main and
// a lane branch are not.
func TestOSParallelStepFollowsTheBranchList(t *testing.T) {
	var script string
	for _, step := range readWorkflow(t, "ci.yml").Jobs["changes"].Steps {
		if step.Name == "Choose the job order for this branch" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("ci.yml has no step that chooses the job order")
	}
	root := repoRoot(t)
	cases := map[string]string{"main": "false", "release/0.7.1": "true", "integration/x": "true", "fix/follow-pi-anthropic-defs": "false", "fix/071-ci": "false", "fix/follow-pi-next-extra": "false"}
	for _, name := range integrationBranches(t) {
		cases[name] = "true"
	}
	for branch, want := range cases {
		output := filepath.Join(t.TempDir(), "output")
		cmd := exec.CommandContext(t.Context(), testenv.Bash(t), "-eo", "pipefail", "-c", script)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "BRANCH="+branch, "GITHUB_OUTPUT="+output)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", branch, err, out)
		}
		got, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(got)) != "os-parallel="+want {
			t.Errorf("branch %q: %q, want os-parallel=%s", branch, got, want)
		}
	}
}
