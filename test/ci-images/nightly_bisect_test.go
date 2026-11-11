// SPDX-License-Identifier: MIT

package ciimages

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A repository of ten commits whose file "state" says ok until commit badFrom and broken from it on, and a probe that reads it.
func bisectFixture(t *testing.T, badFrom int) (dir string, commits []string, probe string) {
	t.Helper()
	dir = t.TempDir()
	runGitIn(t, dir, "init", "-q", "-b", "main")
	for i := 0; i < 10; i++ {
		state := "ok"
		if i >= badFrom {
			state = "broken"
		}
		if err := os.WriteFile(filepath.Join(dir, "state"), []byte(state+fmt.Sprint(i)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGitIn(t, dir, "add", "-A")
		runGitIn(t, dir, "commit", "-q", "-m", fmt.Sprint("c", i))
		commits = append(commits, runGitIn(t, dir, "rev-parse", "HEAD"))
	}
	probe = filepath.Join(t.TempDir(), "probe.sh")
	script := "#!/bin/sh\n# $1 is the commit under test\ncase \"$(git -C \"" + dir + "\" show \"$1:state\")\" in ok*) exit 0;; broken*) exit 1;; *) exit 125;; esac\n"
	if err := os.WriteFile(probe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, commits, probe
}

func runBisect(t *testing.T, dir, probe, good, bad string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "automation", "ci", "nightly-bisect.sh"), good, bad)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PROBE="+probe)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// The nightly bisect names the first commit the probe calls bad, wherever it is in the range.
func TestNightlyBisectFindsTheFirstBadCommit(t *testing.T) {
	for _, badFrom := range []int{1, 4, 9} {
		dir, commits, probe := bisectFixture(t, badFrom)
		got, err := runBisect(t, dir, probe, commits[0], commits[9])
		if err != nil {
			t.Fatalf("badFrom %d: %v", badFrom, err)
		}
		if want := "first-bad=" + commits[badFrom]; got != want {
			t.Errorf("badFrom %d: got %q, want %q", badFrom, got, want)
		}
	}
}

// One commit between the green and the red nightly is the answer without a CI run; no change at all is not a code failure.
func TestNightlyBisectEdgeRanges(t *testing.T) {
	dir, commits, probe := bisectFixture(t, 5)
	if got, err := runBisect(t, dir, "/nonexistent-probe", commits[4], commits[5]); err != nil || got != "first-bad="+commits[5] {
		t.Errorf("adjacent range: got %q, %v", got, err)
	}
	if _, err := runBisect(t, dir, probe, commits[5], commits[5]); err == nil {
		t.Error("a range with the same good and bad commit succeeded")
	}
	if _, err := runBisect(t, dir, probe, commits[7], commits[3]); err == nil {
		t.Error("a good commit that is not an ancestor of the bad one succeeded")
	}
}

// A probe that cannot judge any commit leaves no answer, and the search says so instead of naming a commit.
func TestNightlyBisectFailsWhenTheProbeCannotJudge(t *testing.T) {
	dir, commits, _ := bisectFixture(t, 5)
	skip := filepath.Join(t.TempDir(), "skip.sh")
	if err := os.WriteFile(skip, []byte("#!/bin/sh\nexit 125\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := runBisect(t, dir, skip, commits[0], commits[9]); err == nil {
		t.Errorf("an unjudgeable range named %q", got)
	}
}

// The workflow reacts only to a red scheduled CI run on main, holds the permissions the bisect needs, and merges nothing.
func TestNightlyBisectWorkflowOpensAnIssueAndMergesNothing(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "nightly-bisect.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"workflow_run:", "workflows: [CI]", "workflow_run.conclusion == 'failure'", "workflow_run.event == 'schedule'",
		"workflow_run.head_branch == 'main'", "automation/ci/nightly-bisect.sh", "automation/ci/nightly-issue.sh",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("nightly-bisect.yml lacks %q", want)
		}
	}
	for _, banned := range []string{"gh pr merge", "--auto", "pulls/", "git push origin main", "git revert"} {
		if strings.Contains(text, banned) {
			t.Errorf("nightly-bisect.yml contains %q: the bisect opens an issue and merges nothing", banned)
		}
	}
	for _, name := range []string{"nightly-issue.sh", "nightly-probe.sh"} {
		script, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"gh pr merge", "gh pr create", "--auto"} {
			if strings.Contains(string(script), banned) {
				t.Errorf("%s contains %q", name, banned)
			}
		}
	}
}

// wait-preflight.sh turns the "preflight" commit status of a head into the Pre-flight verdict job's result.
func TestWaitPreflightReportsTheStatusOfTheHead(t *testing.T) {
	script := filepath.Join(repoRoot(t), "automation", "ci", "wait-preflight.sh")
	run := func(states ...string) error {
		bin := t.TempDir()
		count := filepath.Join(bin, "count")
		var cases string
		for i, state := range states {
			cases += fmt.Sprintf("%d) echo %s;;\n", i, state)
		}
		fake := "#!/bin/sh\nn=$(cat \"" + count + "\" 2>/dev/null || echo 0)\necho $((n+1)) > \"" + count + "\"\ncase $n in\n" + cases + "*) echo none;;\nesac\n"
		gh := filepath.Join(bin, "gh")
		if err := os.WriteFile(gh, []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", script, "o/r", "abc")
		cmd.Env = append(os.Environ(), "GH="+gh, "PREFLIGHT_POLL_SECONDS=0", "PREFLIGHT_WAIT_SECONDS=0")
		out, err := cmd.CombinedOutput()
		t.Logf("%v: %s", states, out)
		return err
	}
	if err := run("success"); err != nil {
		t.Errorf("a passed pre-flight failed the verdict: %v", err)
	}
	for _, state := range []string{"failure", "error", "none", "pending", "weird"} {
		if err := run(state); err == nil {
			t.Errorf("state %q passed the verdict", state)
		}
	}
}
