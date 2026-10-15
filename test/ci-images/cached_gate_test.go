package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// cached-gate.sh replays a gate's passing run while every tracked, modified and untracked file is unchanged, restores the gate's output directory, and runs the gate again after any change, after a failure, and under CI.
func TestCachedGateReplaysOnlyAnUnchangedPass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the gate scripts run under bash on Unix runners")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeCIFixture(t, repo, ".gitignore", "/out/\n")
	writeCIFixture(t, repo, "input.txt", "one\n")
	git("add", ".")
	git("commit", "-q", "-m", "fixture")
	// The gate counts its runs in a file outside the repository, writes an output directory, and fails while fail exists.
	runs := filepath.Join(t.TempDir(), "runs")
	gate := `echo run >> "$RUNS"; mkdir -p out; cp input.txt out/copy; echo "gate output"; test ! -e fail`
	t.Setenv("RUNS", runs)
	t.Setenv("PIG_CACHE_HOME", t.TempDir())
	t.Setenv("CI", "")
	t.Setenv("PIG_GATE_CACHE", "")
	script := repoScript(t, "automation/ci/cached-gate.sh")
	run := func() (string, error) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), testenv.Bash(t), script, "fixture", "--out", "out", "--", "bash", "-c", gate)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	count := func() int {
		t.Helper()
		data, err := os.ReadFile(runs)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(data), "run\n")
	}
	step := func(name string, wantRuns int, wantErr bool) string {
		t.Helper()
		out, err := run()
		if (err != nil) != wantErr {
			t.Fatalf("%s: err=%v, want error %v\n%s", name, err, wantErr, out)
		}
		if got := count(); got != wantRuns {
			t.Fatalf("%s: gate ran %d times in total, want %d\n%s", name, got, wantRuns, out)
		}
		if !strings.Contains(out, "gate output") {
			t.Fatalf("%s: output not shown\n%s", name, out)
		}
		return out
	}
	step("first run", 1, false)
	if err := os.RemoveAll(filepath.Join(repo, "out")); err != nil {
		t.Fatal(err)
	}
	if out := step("unchanged inputs", 1, false); !strings.Contains(out, "[cached-gate] fixture: unchanged inputs") {
		t.Fatalf("replay not reported:\n%s", out)
	}
	if data, err := os.ReadFile(filepath.Join(repo, "out", "copy")); err != nil || string(data) != "one\n" {
		t.Fatalf("output directory not restored: %q, %v", data, err)
	}
	writeCIFixture(t, repo, "input.txt", "two\n")
	step("modified tracked file", 2, false)
	step("same modification", 2, false)
	writeCIFixture(t, repo, "new.txt", "x\n")
	step("new untracked file", 3, false)
	writeCIFixture(t, repo, "fail", "")
	step("failing gate", 4, true)
	step("failure is not stored", 5, true)
	if err := os.Remove(filepath.Join(repo, "fail")); err != nil {
		t.Fatal(err)
	}
	step("pass with the earlier inputs", 5, false)
	t.Setenv("CI", "true")
	step("CI always runs", 6, false)
}
