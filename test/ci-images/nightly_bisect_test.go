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
	for i := range 10 {
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

// runWaitPreflight runs wait-preflight.sh against a fake gh: the workflow on the default branch main is present, absent (HTTP 404), or an
// error; a lookup on any other ref is an error, so a skip proves main was asked. states are the successive statuses.
func runWaitPreflight(t *testing.T, onMain string, states ...string) (string, error) {
	script := filepath.Join(repoRoot(t), "automation", "ci", "wait-preflight.sh")
	bin := t.TempDir()
	count := filepath.Join(bin, "count")
	var cases string
	for i, state := range states {
		cases += fmt.Sprintf("%d) echo %s;;\n", i, state)
	}
	fake := "#!/bin/sh\ncase \"$*\" in *contents/*preflight.yml?ref=main*) case \"$FAKE_MAIN\" in present) echo file; exit 0;; absent) echo \"gh: Not Found (HTTP 404)\" >&2; exit 1;; *) echo \"gh: boom (HTTP 502)\" >&2; exit 1;; esac;; *contents/*) echo \"gh: Not Found (HTTP 404)\" >&2; exit 1;; esac\nn=$(cat \"" + count + "\" 2>/dev/null || echo 0)\necho $((n+1)) > \"" + count + "\"\ncase $n in\n" + cases + "*) echo none;;\nesac\n"
	gh := filepath.Join(bin, "gh")
	if err := os.WriteFile(gh, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(bin, "summary")
	cmd := exec.Command("bash", script, "o/r", "abc", "main")
	cmd.Env = append(os.Environ(), "FAKE_MAIN="+onMain, "GITHUB_STEP_SUMMARY="+summary, "GH="+gh, "PREFLIGHT_POLL_SECONDS=0", "PREFLIGHT_WAIT_SECONDS=0")
	out, err := cmd.CombinedOutput()
	t.Logf("%v: %s", states, out)
	note, _ := os.ReadFile(summary)
	return string(out) + string(note), err
}

// wait-preflight.sh turns the "preflight" commit status of a head into the Pre-flight verdict job's result.
func TestWaitPreflightReportsTheStatusOfTheHead(t *testing.T) {
	run := func(states ...string) error {
		_, err := runWaitPreflight(t, "present", states...)
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

// The wait is skipped only when the default branch lacks the workflow (pull_request_target runs from it, whatever the base);
// a head that deletes it, or an API error, keeps the wait.
func TestWaitPreflightSkipsOnlyWhenTheDefaultBranchLacksTheWorkflow(t *testing.T) {
	out, err := runWaitPreflight(t, "absent", "none")
	if err != nil || !strings.Contains(out, "pre-flight is not on the default branch yet: verdict skipped") {
		t.Errorf("default branch without the workflow: err %v, output %q; want a skip with the notice", err, out)
	}
	for _, onMain := range []string{"present", "error"} {
		out, err := runWaitPreflight(t, onMain, "none")
		if err == nil || strings.Contains(out, "verdict skipped") {
			t.Errorf("workflow %q on main skipped the wait: err %v, output %q", onMain, err, out)
		}
	}
	if _, err := runWaitPreflight(t, "present", "success"); err != nil {
		t.Errorf("main with the workflow and a passed status failed: %v", err)
	}
}

// A rerun keeps the run id, and the earlier attempt's CI run stays listed on its deleted branch. The probe branch
// carries the attempt as well, so the probe's lookup by branch finds only the run it dispatched.
func TestNightlyBisectProbeBranchIsUniquePerRunAttempt(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "nightly-bisect.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if name, value, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && name == "BISECT_TAG" {
			tags = append(tags, strings.TrimSpace(value))
		}
	}
	if len(tags) != 1 || tags[0] != "${{ github.run_id }}-${{ github.run_attempt }}" {
		t.Fatalf("BISECT_TAG is %q, want the run id and the run attempt", tags)
	}
	probe, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "nightly-probe.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(probe), `branch="bisect/${BISECT_TAG:?run tag}-${sha:0:12}"`) {
		t.Fatal("nightly-probe.sh no longer names its branch after BISECT_TAG")
	}
}

// The range can reach back before the probe script existed, and git bisect checks those commits out. The default probe
// is run from a copy outside the checkout, so every commit of the range is judged by the same probe.
func TestNightlyBisectKeepsTheDefaultProbeWhenOldCommitsLackIt(t *testing.T) {
	dir := t.TempDir()
	runGitIn(t, dir, "init", "-q", "-b", "main")
	bisect, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "nightly-bisect.sh"))
	if err != nil {
		t.Fatal(err)
	}
	probe := "#!/bin/sh\ncase \"$(git show \"$1:state\")\" in ok*) exit 0;; broken*) exit 1;; *) exit 125;; esac\n"
	const badFrom, scriptsFrom = 3, 8
	var commits []string
	for i := range 10 {
		state := "ok"
		if i >= badFrom {
			state = "broken"
		}
		if err := os.WriteFile(filepath.Join(dir, "state"), []byte(state+fmt.Sprint(i)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if i == scriptsFrom {
			scripts := filepath.Join(dir, "automation", "ci")
			if err := os.MkdirAll(scripts, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, text := range map[string]string{"nightly-bisect.sh": string(bisect), "nightly-probe.sh": probe} {
				if err := os.WriteFile(filepath.Join(scripts, name), []byte(text), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
		runGitIn(t, dir, "add", "-A")
		runGitIn(t, dir, "commit", "-q", "-m", fmt.Sprint("c", i))
		commits = append(commits, runGitIn(t, dir, "rev-parse", "HEAD"))
	}
	// As the workflow runs it: the checkout's own script, no PROBE in the environment.
	cmd := exec.Command("bash", "automation/ci/nightly-bisect.sh", commits[0], commits[9])
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PROBE=")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if got, want := strings.TrimSpace(string(out)), "first-bad="+commits[badFrom]; err != nil || got != want {
		t.Fatalf("got %q, %v, want %q\n%s", got, err, want, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "automation", "ci", "nightly-probe.sh")); err != nil {
		t.Errorf("the bisect did not return to the commit it started on: %v", err)
	}
}

// runNightlyIssue runs nightly-issue.sh against a fake gh that answers "run view" with the jobs of a red run, filtered
// by the script's own jq expression, and returns the body of the issue the script created ("" when it created none).
func runNightlyIssue(t *testing.T, jobs string, args ...string) (body, calls string) {
	t.Helper()
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq is not on PATH")
	}
	dir, commits, _ := bisectFixture(t, 5)
	bin := t.TempDir()
	fake := `#!/bin/sh
echo "$*" >> "$FAKE_DIR/calls"
case "$1 $2" in
"issue list") ;;
"run view")
  [ -n "$FAKE_JOBS" ] || { echo "gh: run not found" >&2; exit 1; }
  while [ "$#" -gt 1 ]; do [ "$1" = --jq ] && filter=$2; shift; done
  printf '%s' "$FAKE_JOBS" | "$FAKE_JQ" -r "$filter" ;;
"issue create")
  while [ "$#" -gt 1 ]; do [ "$1" = --body ] && printf '%s\n' "$2" > "$FAKE_DIR/body"; shift; done ;;
*) echo "unexpected gh $*" >&2; exit 1 ;;
esac
`
	gh := filepath.Join(bin, "gh")
	if err := os.WriteFile(gh, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{filepath.Join(repoRoot(t), "automation", "ci", "nightly-issue.sh"), commits[5], commits[0], commits[9]}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "REPO=o/r", "GH="+gh, "FAKE_DIR="+bin, "FAKE_JOBS="+jobs, "FAKE_JQ="+jq)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nightly-issue.sh: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(filepath.Join(bin, "body"))
	log, _ := os.ReadFile(filepath.Join(bin, "calls"))
	return string(raw), string(log)
}

// The issue names the jobs of the red nightly run that failed, each with its link, and no job that passed.
func TestNightlyIssueListsTheFailedJobsOfTheRedRun(t *testing.T) {
	jobs := `{"jobs":[
		{"name":"Lint","conclusion":"success","url":"https://github.example/o/r/actions/runs/77/job/1"},
		{"name":"Unit tests (windows) [2/4]","conclusion":"failure","url":"https://github.example/o/r/actions/runs/77/job/2"},
		{"name":"Parity","conclusion":"timed_out","url":"https://github.example/o/r/actions/runs/77/job/3"},
		{"name":"CI result","conclusion":"skipped","url":"https://github.example/o/r/actions/runs/77/job/4"},
		{"name":"Race","conclusion":"cancelled","url":"https://github.example/o/r/actions/runs/77/job/5"}]}`
	body, calls := runNightlyIssue(t, jobs, "https://github.example/o/r/actions/runs/77", "77")
	if !strings.Contains(calls, "run view 77 --repo o/r --json jobs") {
		t.Fatalf("the red run's jobs were not read:\n%s", calls)
	}
	for _, want := range []string{
		"- red run: https://github.example/o/r/actions/runs/77",
		"- failed jobs of the red run:",
		"  - Unit tests (windows) [2/4] (failure): https://github.example/o/r/actions/runs/77/job/2",
		"  - Parity (timed_out): https://github.example/o/r/actions/runs/77/job/3",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the issue lacks %q:\n%s", want, body)
		}
	}
	for _, banned := range []string{"Lint", "CI result", "Race"} {
		if strings.Contains(body, banned) {
			t.Errorf("the issue names %q, which did not fail:\n%s", banned, body)
		}
	}
}

// The issue is the request for the fix: it is opened when the jobs cannot be read, and says so; a manual bisect has no
// red run and names no jobs.
func TestNightlyIssueIsOpenedWithoutTheJobs(t *testing.T) {
	body, _ := runNightlyIssue(t, "", "https://github.example/o/r/actions/runs/77", "77")
	if !strings.Contains(body, "- failed jobs of the red run: could not be read; see the run") || !strings.Contains(body, "first bad commit") {
		t.Errorf("unreadable jobs:\n%s", body)
	}
	body, calls := runNightlyIssue(t, "")
	if strings.Contains(body, "failed jobs") || strings.Contains(calls, "run view") || !strings.Contains(body, "first bad commit") {
		t.Errorf("a bisect without a red run: calls\n%s\nbody\n%s", calls, body)
	}
}

// The workflow hands the red run's id to the issue script.
func TestNightlyBisectWorkflowPassesTheRedRunToTheIssue(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "nightly-bisect.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `automation/ci/nightly-issue.sh "$FIRST" "$GOOD" "$BAD" "${RED_URL:-}" "${RED_RUN:-}"`) {
		t.Fatal("nightly-bisect.yml does not pass the red run's id to nightly-issue.sh")
	}
}
