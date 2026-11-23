// SPDX-License-Identifier: MIT

package ciimages

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// changedRunDeadline bounds the script under test. A run that does not finish fails with its output instead of waiting for go test's package timeout.
const changedRunDeadline = 30 * time.Second

// hermeticKeep names the variables of the test process the script under test may see besides the ones the test sets itself: the tool locations and
// locale every shell needs, and the Windows system variables Git Bash needs. Everything else (GO, GOFLAGS, GOTOOLCHAIN, CI, TEST_BASE, the TEST_CHANGED_*
// and PIG_* settings of the run the test sits in) is dropped.
var hermeticKeep = []string{"HOME", "USERPROFILE", "SystemRoot", "SYSTEMROOT", "WINDIR", "COMSPEC", "OS", "MSYS", "MSYSTEM", "LANG", "LC_ALL", "TMP", "TEMP"}

// changedGoTestLines runs `test-grouped.sh changed` in a fresh fixture root, so under its own checkout lock, with the fixture's fake go first on PATH and a
// poisoned go after it, a fake changed-packages.py that selects one edited package, and an environment built from nothing (leak sets the CI job's
// variables in the test process first, to prove they do not reach the script): the poisoned go records a call and fails, so a script that reaches a
// real go fails the test. It returns the go test command lines the fake recorded.
func changedGoTestLines(t *testing.T, leak bool, env ...string) []string {
	t.Helper()
	root := groupedTestFixture(t)
	if leak {
		// After the fixture, which clears three of these for the plain runs of the other tests: the script must not see them either way.
		leakTheCIJobsEnvironment(t)
	}
	copyCIFixture(t, root, "automation/ci/test-json-report.py")
	writeCIFixture(t, root, "automation/ci/changed-packages.py", "print('direct ./alpha')\nprint('affected ./alpha')\n")
	poison := t.TempDir()
	reached := filepath.Join(poison, "reached")
	writeCIFixture(t, poison, "go", "#!/bin/sh\necho \"$0 $*\" >> '"+reached+"'\nexit 99\n")
	argsLog := filepath.Join(root, "args")
	writeCIFixture(t, root, "args", "")

	childEnv := []string{
		"PATH=" + filepath.Join(root, "bin") + string(os.PathListSeparator) + poison + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + t.TempDir(),
		"TEST_LOG=" + filepath.Join(root, "packages"),
		"ARGS_LOG=" + argsLog,
		"TEST_BASE=HEAD",
	}
	for _, name := range hermeticKeep {
		if value, ok := os.LookupEnv(name); ok && !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, name+"=") }) {
			childEnv = append(childEnv, name+"="+value)
		}
	}
	childEnv = append(childEnv, env...)

	ctx, cancel := context.WithTimeout(t.Context(), changedRunDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, testenv.Bash(t), filepath.Join(root, "automation/ci/test-grouped.sh"), "changed")
	cmd.Dir = root
	cmd.Env = childEnv
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test-grouped.sh changed (deadline %s): %v\n%s", changedRunDeadline, err, output)
	}
	if data, err := os.ReadFile(reached); err == nil {
		t.Fatalf("the script reached a go that is not the fixture's:\n%s", data)
	}
	data, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("no go test ran: %v\n%s", err, output)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// leakTheCIJobsEnvironment sets, in the test process, what the CI job's `make ci-test-changed` exports to the tests it runs: its own test-grouped.sh run
// sets TEST_CHANGED_FOR and TEST_CHANGED_RACE=none, CI=true, and the runner names a real go through GO, GOROOT and GOTOOLCHAIN. None of it may reach the script
// the test starts, and the lock the outer run holds on the checkout is not the fixture root's.
func leakTheCIJobsEnvironment(t *testing.T) {
	t.Helper()
	for name, value := range map[string]string{
		"CI": "true", "GITHUB_ACTIONS": "true", "TEST_CHANGED_FOR": "fast-parallel", "TEST_CHANGED_RACE": "none", "TEST_BASE": "origin/main",
		"GO": "/nonexistent/go", "GOROOT": "/nonexistent/goroot", "GOTOOLCHAIN": "go0.0.0", "GOFLAGS": "-bogusflag", "PIG_RACE_TIMEOUT": "1ns",
		"PIG_TEST_JSON_DIR": "/nonexistent", "GITHUB_STEP_SUMMARY": "/nonexistent/summary", "TEST_FAST_PARALLEL": "99",
	} {
		t.Setenv(name, value)
	}
}

func TestGroupedChangedRaceRunCarriesTheRaceTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's fake go is a bash script found through PATH")
	}
	for _, tc := range []struct {
		name string
		leak bool
		env  []string
		want string
	}{
		{name: "default", want: "-timeout 60m"},
		{name: "override", env: []string{"PIG_RACE_TIMEOUT=90m"}, want: "-timeout 90m"},
		{name: "default in the CI job's environment", leak: true, want: "-timeout 60m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := changedGoTestLines(t, tc.leak, tc.env...)
			if len(lines) != 1 || !strings.Contains(lines[0], " -race ") || !strings.Contains(lines[0], tc.want+" ") {
				t.Fatalf("race run = %q, want -race and %q", lines, tc.want)
			}
		})
	}
}

func TestGroupedChangedPlainRunKeepsTheDefaultTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's fake go is a bash script found through PATH")
	}
	for _, leak := range []bool{false, true} {
		lines := changedGoTestLines(t, leak, "TEST_CHANGED_RACE=none", "PIG_RACE_TIMEOUT=90m")
		if len(lines) != 1 || strings.Contains(lines[0], "-race") || !strings.Contains(lines[0], " -timeout 30m ") {
			t.Fatalf("plain run (leaked CI environment %v) = %q, want no -race and plain -timeout 30m", leak, lines)
		}
	}
}
