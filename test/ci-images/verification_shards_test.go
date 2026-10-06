// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package ciimages

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type verificationJob struct {
	Name     string
	Needs    []string
	If       string
	Timeout  int `yaml:"timeout-minutes"`
	Strategy struct {
		FailFast *bool `yaml:"fail-fast"`
		Matrix   struct{ Shard []string }
	}
	Steps []struct {
		If  string
		Run string
		Env map[string]string
	}
}

func verificationJobs(t *testing.T) map[string]verificationJob {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct{ Jobs map[string]verificationJob }
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow.Jobs
}

func TestLinuxShardsRetainEveryCheck(t *testing.T) {
	jobs := verificationJobs(t)
	linux := jobs["linux"]
	if len(linux.Strategy.Matrix.Shard) < 2 {
		t.Fatal("Linux verification is not sharded")
	}
	if linux.Timeout > 60 || linux.Timeout <= 0 || linux.Strategy.FailFast == nil || *linux.Strategy.FailFast {
		t.Fatal("Linux shards must retain the timeout ceiling and run every shard on failure")
	}
	invokesShard := false
	for _, step := range linux.Steps {
		if step.Env["SHARD"] == "${{ matrix.shard }}" && strings.Contains(step.Run, `make "ci-$SHARD"`) {
			invokesShard = true
		}
	}
	if !invokesShard {
		t.Fatal("matrix does not invoke the checked Make targets")
	}

	// The independent denominator is make check, not a snapshot count of jobs.
	rules := map[string][]string{}
	for line := range strings.SplitSeq(readMakeSources(t, repoRoot(t)), "\n") {
		if strings.HasPrefix(line, "\t") {
			continue
		}
		line, _, _ = strings.Cut(line, "#")
		name, deps, ok := strings.Cut(line, ":")
		if ok && !strings.ContainsAny(name, " =$()") {
			rules[name] = strings.Fields(deps)
		}
	}
	var leaves func(string) []string
	leaves = func(name string) []string {
		switch name {
		case "check", "check-core", "check-contracts-fast":
		case "test":
			return []string{"test-fast", "test-cli", "test-subprocess", "test-conformance"}
		default:
			if !strings.HasPrefix(name, "ci-") {
				return []string{name}
			}
		}
		deps, ok := rules[name]
		if !ok {
			t.Fatalf("missing Make rule %s", name)
		}
		var result []string
		for _, dep := range deps {
			result = append(result, leaves(dep)...)
		}
		return result
	}
	want := leaves("check")
	var got []string
	for _, shard := range linux.Strategy.Matrix.Shard {
		got = append(got, leaves("ci-"+shard)...)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("shards must partition all make check gates exactly once\ngot  %v\nwant %v", got, want)
	}
}

func TestLinuxAggregateRejectsUnsuccessfulShards(t *testing.T) {
	job := verificationJobs(t)["verify"]
	if !slices.Contains(job.Needs, "linux") || !strings.Contains(job.If, "always()") {
		t.Fatal("Linux aggregate must run even when matrix jobs fail or are skipped")
	}
	var script string
	for _, step := range job.Steps {
		if step.Env["RESULT"] == "${{ needs.linux.result }}" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("Linux aggregate does not consume matrix result")
	}
	for _, result := range []string{"success", "failure", "cancelled", "skipped", ""} {
		t.Run(result, func(t *testing.T) {
			t.Setenv("RESULT", result)
			cmd := exec.CommandContext(t.Context(), testenv.Bash(t), "-euo", "pipefail", "-c", script)
			output, err := cmd.CombinedOutput()
			if (err == nil) != (result == "success") {
				t.Fatalf("aggregate for %q: %v\n%s", result, err, output)
			}
		})
	}
}

// fakeTests are the top-level tests the fixture's fake go test -list prints for every package; it also prints a benchmark, which -run does not select.
var fakeTests = func() []string {
	var names []string
	for i := range 24 {
		names = append(names, fmt.Sprintf("TestFixture%02d", i))
	}
	return append(names, "ExampleFixture", "FuzzFixture")
}()

// groupedTestFixture copies test-grouped.sh and the scripts it runs into a fresh root whose fake go lists five packages, lists fakeTests for go test -list, and appends the packages each go test receives to $TEST_LOG and each -run pattern, after its package, to $RUN_LOG. LIST_FAIL fails the listing, LIST_EXTRA adds a name to it, and FAIL_TEST fails each go test whose -run pattern names that test. When LEAK_TMP is set, the fake go test leaves a file in the TMPDIR it runs under, as a leaking test package would. When WIPE_AGENT_AUTH is set it rewrites auth.json in the agent directory its environment names, as the 2026-10-06 lane wipe did, and RECORD_AGENT_ENV names a file that receives the agent environment each go test sees.
func groupedTestFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	copyCIFixture(t, root, "automation/ci/test-grouped.sh")
	copyCIFixture(t, root, "automation/ci/test-shard-pattern.sh")
	copyCIFixture(t, root, "automation/ci/assert-clean-tmp.sh")
	copyCIFixture(t, root, "automation/ci/agent-dir-guard.sh")
	// test-grouped.sh enters the in-repo Porter extension module to warm its dependency-module build cache.
	writeCIFixture(t, root, "piglets/porter/extensions/pig-porter/.keep", "")
	writeCIFixture(t, root, "automation/ci/test-fixtures.sh", "#!/bin/sh\nprintf 'export CI_TEST_FIXTURES=ready\\n'\n")
	writeCIFixture(t, root, "bin/go", `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  list)
    shift
    if [[ "$*" == ./... ]]; then
      printf '%s\n' ./alpha ./beta ./cmd/pig ./coding/extension/host/subprocess ./test/extension-conformance
    else
      printf '%s\n' "$@"
    fi
    ;;
  build) ;;
  test)
    if [[ "$2" == -list ]]; then
      if [[ -n "${LIST_FAIL:-}" ]]; then echo TestPartial; exit 1; fi
      printf '%s\n' `+strings.Join(fakeTests, " ")+` BenchmarkFixture ${LIST_EXTRA:-}
      printf 'ok  \t%s\t0.01s\n' "$4"
      exit 0
    fi
    test "$CI_TEST_FIXTURES" = ready
    shift 3
    packages=() run=
    while (($#)); do
      case "$1" in
        -run) run=$2; shift 2 ;;
        -*) shift ;;
        *) packages+=("$1"); shift ;;
      esac
    done
    printf '%s\n' "${packages[@]}" >> "$TEST_LOG"
    if [[ -n "$run" ]]; then printf '%s %s\n' "${packages[*]}" "$run" >> "$RUN_LOG"; fi
    if [[ -n "${FAIL_TEST:-}" && "$run" == *"$FAIL_TEST"* ]]; then exit 1; fi
    if [[ -n "${LEAK_TMP:-}" ]]; then : > "$TMPDIR/pig-leak"; fi
    if [[ -n "${WIPE_AGENT_AUTH:-}" ]]; then printf '{}' > "$PIG_CODING_AGENT_DIR/auth.json"; fi
    # Under Git Bash this script sees POSIX paths (/tmp/... for the Windows temporary directory) where the Go test, and a real go.exe, see Windows paths. Record each directory in the host's own form: pwd -W prints it there, and is rejected on every other bash.
    native() { (cd "$1" 2>/dev/null && { pwd -W 2>/dev/null || pwd; }) || printf '%s\n' "$1"; }
    if [[ -n "${RECORD_AGENT_ENV:-}" ]]; then printf '%s\n' "$(native "$PIG_CODING_AGENT_DIR")" "$(native "$PI_CODING_AGENT_DIR")" "$(native "$PIG_HOME")" "$(native "$PI_HOME")" "${PIG_CODING_AGENT_SESSION_DIR-unset}" >> "$RECORD_AGENT_ENV"; fi
    ;;
  *) exit 99 ;;
esac
`)
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root
}

func TestLinuxTestShardsPartitionPackages(t *testing.T) {
	root := groupedTestFixture(t)
	log := filepath.Join(root, "packages")
	t.Setenv("TEST_LOG", log)
	t.Setenv("RUN_LOG", filepath.Join(root, "runs"))
	var defaultPackages []string
	for _, modes := range [][]string{{"default"}, {"fast", "cli", "subprocess", "conformance"}} {
		writeCIFixture(t, root, "packages", "")
		for _, mode := range modes {
			cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/test-grouped.sh"), mode)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("test mode %s: %v\n%s", mode, err, output)
			}
		}
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		packages := strings.Fields(string(data))
		slices.Sort(packages)
		if defaultPackages == nil {
			defaultPackages = packages
		} else if !slices.Equal(packages, defaultPackages) {
			t.Fatalf("shards must partition default tests exactly once: got %v, want %v", packages, defaultPackages)
		}
	}
	// Each shard must retain make test's preparation rather than invoking go test bare.
	makefile := readMakeSources(t, repoRoot(t))
	for _, mode := range []string{"fast", "cli", "subprocess", "conformance"} {
		pattern := `(?m)^test-` + mode + `: test-prereqs interface-deps parity-deps[^\n]*\n\t@\./automation/ci/test-grouped.sh ` + mode + `$`
		if !regexp.MustCompile(pattern).MatchString(makefile) {
			t.Errorf("test-%s does not preserve test preparation and package selection", mode)
		}
	}
}

// cliShards reads the cmd/pig shard count and package that test-grouped.sh runs.
func cliShards(t *testing.T) (int, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "test-grouped.sh"))
	if err != nil {
		t.Fatal(err)
	}
	count := regexp.MustCompile(`(?m)^CLI_SHARDS=([0-9]+)$`).FindSubmatch(data)
	pkg := regexp.MustCompile(`(?m)^CLI_PKG=(\S+)$`).FindSubmatch(data)
	if count == nil || pkg == nil {
		t.Fatal("test-grouped.sh does not declare CLI_SHARDS and CLI_PKG")
	}
	shards, err := strconv.Atoi(string(count[1]))
	if err != nil || shards < 2 {
		t.Fatalf("CLI_SHARDS=%s: want two or more shards", count[1])
	}
	return shards, string(pkg[1])
}

// requireShardPartition fails unless the -run patterns select every runnable fake test exactly once.
func requireShardPartition(t *testing.T, patterns []string) {
	t.Helper()
	for _, name := range fakeTests {
		matches := 0
		for _, pattern := range patterns {
			if regexp.MustCompile(pattern).MatchString(name) {
				matches++
			}
		}
		if matches != 1 {
			t.Errorf("%s is selected by %d shards, want exactly one", name, matches)
		}
	}
}

// make test-cli runs cmd/pig as CLI_SHARDS go test processes, each under its own package timeout, whose -run patterns partition the package's tests. A failing shard does not stop the later ones.
func TestCLIShardsPartitionTheCLIPackage(t *testing.T) {
	shards, pkg := cliShards(t)
	root := groupedTestFixture(t)
	runs := filepath.Join(root, "runs")
	t.Setenv("TEST_LOG", filepath.Join(root, "packages"))
	t.Setenv("RUN_LOG", runs)
	run := func() ([]string, error) {
		t.Helper()
		writeCIFixture(t, root, "runs", "")
		cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/test-grouped.sh"), "cli")
		output, err := cmd.CombinedOutput()
		data, readErr := os.ReadFile(runs)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var patterns []string
		for line := range strings.Lines(string(data)) {
			gotPkg, pattern, ok := strings.Cut(strings.TrimSpace(line), " ")
			if !ok || gotPkg != pkg {
				t.Fatalf("shard ran %q, want package %s with a -run pattern\n%s", line, pkg, output)
			}
			patterns = append(patterns, pattern)
		}
		if err != nil {
			err = fmt.Errorf("%w\n%s", err, output)
		}
		return patterns, err
	}
	patterns, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != shards {
		t.Fatalf("cli ran %d go test processes, want %d shards", len(patterns), shards)
	}
	requireShardPartition(t, patterns)

	// Fail a test of the first shard, so every later shard must still run.
	first, _, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(patterns[0], "^("), ")$"), "|")
	if !slices.Contains(fakeTests, first) {
		t.Fatalf("first shard pattern %q does not start with a fixture test", patterns[0])
	}
	t.Setenv("FAIL_TEST", first)
	patterns, err = run()
	if err == nil {
		t.Fatal("cli passed with a failing shard")
	}
	if len(patterns) != shards {
		t.Fatalf("cli ran %d of %d shards after a failing shard", len(patterns), shards)
	}
	requireShardPartition(t, patterns)
}

// test-shard-pattern.sh fails instead of printing a pattern when the listing fails, the listing names a test it cannot select, a shard is empty, or the arguments are not a shard of a shard count.
func TestShardPatternFailsClosed(t *testing.T) {
	root := groupedTestFixture(t)
	script := filepath.Join(root, "automation/ci/test-shard-pattern.sh")
	shards, _ := cliShards(t)
	for i := 1; i <= shards; i++ {
		output, err := exec.CommandContext(t.Context(), testenv.Bash(t), script, strconv.Itoa(i), strconv.Itoa(shards), "./pkg").Output()
		if err != nil || !regexp.MustCompile(`^\^\((\w+\|)*\w+\)\$\n$`).Match(output) {
			t.Fatalf("shard %d of %d: %v, %q", i, shards, err, output)
		}
	}
	for name, tc := range map[string]struct {
		args []string
		env  string
	}{
		"listing fails":       {[]string{"1", "2", "./pkg"}, "LIST_FAIL=1"},
		"non-ASCII test name": {[]string{"1", "2", "./pkg"}, "LIST_EXTRA=Test\u00dcber"},
		"empty shard":         {[]string{"1", "1000", "./pkg"}, ""},
		"shard beyond count":  {[]string{"3", "2", "./pkg"}, ""},
		"shard zero":          {[]string{"0", "2", "./pkg"}, ""},
		"missing package":     {[]string{"1", "2"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), testenv.Bash(t), append([]string{script}, tc.args...)...)
			cmd.Env = append(os.Environ(), tc.env)
			output, err := cmd.Output()
			if err == nil || len(output) != 0 {
				t.Fatalf("err=%v output=%q, want a failure with no pattern", err, output)
			}
		})
	}
}

// Each group runs go test under a private temporary directory, fails when the run leaves anything in it, and removes it either way, so a leaking package fails make test instead of filling the shared temporary directory.
func TestGroupedTestsFailOnTemporaryLeakAndRemoveScratch(t *testing.T) {
	root := groupedTestFixture(t)
	t.Setenv("TEST_LOG", filepath.Join(root, "packages"))
	outer := t.TempDir()
	t.Setenv("TMPDIR", outer)
	run := func() (string, error) {
		cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/test-grouped.sh"), "fast")
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	requireScratchRemoved := func() {
		t.Helper()
		if left, err := os.ReadDir(outer); err != nil || len(left) != 0 {
			t.Fatalf("group scratch directory left in TMPDIR: %v, %v", left, err)
		}
	}
	if output, err := run(); err != nil {
		t.Fatalf("clean run failed: %v\n%s", err, output)
	}
	requireScratchRemoved()
	t.Setenv("LEAK_TMP", "1")
	output, err := run()
	if err == nil || !strings.Contains(output, "[assert-clean-tmp]") || !strings.Contains(output, "pig-leak") {
		t.Fatalf("leaking run: err=%v, want the leak reported\n%s", err, output)
	}
	requireScratchRemoved()
}

// isUnderDir reports whether path is dir or lies beneath it. It compares file identity, not spelling: Git Bash names the Windows temporary directory /tmp/... while Go names it C:\\..., and a path may use forward slashes or 8.3 short names, so no string prefix test holds on every host.
func isUnderDir(t testing.TB, path, dir string) bool {
	t.Helper()
	want, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %q: %v", dir, err)
	}
	for p := filepath.Clean(path); ; {
		if info, err := os.Stat(p); err == nil && os.SameFile(info, want) {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// A directory is under another by identity, whatever path spells it: through a symlink, with a trailing slash or dot elements, as a Windows host spells the same directory differently from Git Bash.
func TestIsUnderDirComparesFileIdentity(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "a", "b")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(outer, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for name, tc := range map[string]struct {
		path string
		dir  string
		want bool
	}{
		"same directory":         {outer, outer, true},
		"nested":                 {inner, outer, true},
		"alias of the root":      {filepath.Join(alias, "a", "b"), outer, true},
		"dot elements":           {inner + string(filepath.Separator) + ".", outer, true},
		"forward slashes":        {filepath.ToSlash(inner), outer, true},
		"sibling directory":      {other, outer, false},
		"missing path":           {filepath.Join(other, "missing"), outer, false},
		"prefix sibling by name": {outer + "-sibling", outer, false},
	} {
		if got := isUnderDir(t, tc.path, tc.dir); got != tc.want {
			t.Errorf("%s: isUnderDir(%q, %q) = %v, want %v", name, tc.path, tc.dir, got, tc.want)
		}
	}
}

// Each group runs go test with PIG_CODING_AGENT_DIR, PI_CODING_AGENT_DIR, PIG_HOME and PI_HOME pointing at seeded throwaway directories, whatever the caller exported, and fails when a test run changes them. A lane exports the real agent directory for its own pig, and a test that wrote it emptied the lane's credentials.
func TestGroupedTestsIsolateAndGuardTheAgentDirectories(t *testing.T) {
	t.Run("temporary directory as the host spells it", func(t *testing.T) {
		outer := t.TempDir()
		testGroupedAgentDirectoryIsolation(t, outer, outer)
	})
	// Git Bash on Windows spells the temporary directory /tmp/... to the scripts and C:\... to Go. A symlink reproduces that mismatch on every host: the scripts and the fake go see the alias, the test sees the real directory.
	t.Run("temporary directory reached through an alias", func(t *testing.T) {
		outer := t.TempDir()
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(outer, alias); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		testGroupedAgentDirectoryIsolation(t, outer, alias)
	})
}

// testGroupedAgentDirectoryIsolation runs test-grouped.sh with TMPDIR set to tmpdir, a spelling of the directory outer.
func testGroupedAgentDirectoryIsolation(t *testing.T, outer, tmpdir string) {
	t.Helper()
	root := groupedTestFixture(t)
	t.Setenv("TEST_LOG", filepath.Join(root, "packages"))
	t.Setenv("TMPDIR", tmpdir)
	realAgent := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", realAgent)
	t.Setenv("PIG_CODING_AGENT_SESSION_DIR", realAgent)
	record := filepath.Join(root, "agent-env")
	t.Setenv("RECORD_AGENT_ENV", record)
	run := func() (string, error) {
		cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/test-grouped.sh"), "fast")
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	if output, err := run(); err != nil {
		t.Fatalf("clean run failed: %v\n%s", err, output)
	}
	seen, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(seen)), "\n") {
		if line == "unset" {
			continue
		}
		if isUnderDir(t, line, realAgent) || !isUnderDir(t, line, outer) {
			t.Fatalf("go test saw %q, want a directory under the group's private root, not the caller's %q", line, realAgent)
		}
	}
	if !strings.Contains(string(seen), "unset") {
		t.Fatalf("PIG_CODING_AGENT_SESSION_DIR reached go test: %q", seen)
	}
	if left, err := os.ReadDir(outer); err != nil || len(left) != 0 {
		t.Fatalf("agent guard directories left in TMPDIR: %v, %v", left, err)
	}
	t.Setenv("WIPE_AGENT_AUTH", "1")
	output, err := run()
	if err == nil || !strings.Contains(output, "[agent-dir-guard]") || !strings.Contains(output, "agent/auth.json") {
		t.Fatalf("wiping run: err=%v, want the agent-dir guard to report auth.json\n%s", err, output)
	}
	if left, err := os.ReadDir(outer); err != nil || len(left) != 0 {
		t.Fatalf("agent guard directories left in TMPDIR after a failure: %v, %v", left, err)
	}
	if info, err := os.Stat(realAgent); err != nil || !info.IsDir() {
		t.Fatalf("caller's agent directory: %v", err)
	}
}

// A group whose agent directories cannot be seeded must fail without running go test: the caller's real agent directory is still exported, and the guard could not detect a write to it.
func TestGroupedTestsDoNotRunWhenTheAgentDirectoriesCannotBeSeeded(t *testing.T) {
	root := groupedTestFixture(t)
	writeCIFixture(t, root, "automation/ci/agent-dir-guard.sh", "#!/bin/sh\nif [ \"$1\" = seed ]; then exit 3; fi\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "automation/ci/agent-dir-guard.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	testLog := filepath.Join(root, "packages")
	t.Setenv("TEST_LOG", testLog)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/test-grouped.sh"), "fast")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "could not seed isolated agent directories") {
		t.Fatalf("seed failure: err=%v, want the group to fail and say why\n%s", err, output)
	}
	if ran, readErr := os.ReadFile(testLog); readErr == nil && len(ran) > 0 {
		t.Fatalf("go test ran with the caller's agent directory exported: %q", ran)
	}
}

func TestHostedJobsInstallPinnedNpmBeforeOracle(t *testing.T) {
	for name, job := range verificationJobs(t) {
		if name != "linux" && name != "windows" && name != "macos" {
			continue
		}
		installed := false
		verified := false
		oracle := false
		for _, step := range job.Steps {
			for line := range strings.SplitSeq(step.Run, "\n") {
				if strings.Contains(line, "npm ci --prefix automation/ci/npm-toolchain") {
					installed = true
				}
				if strings.Contains(line, `test "$(npm --version)" = "$NPM_VERSION"`) {
					verified = installed
				}
				if strings.Contains(line, "make upstream-mirror") || strings.Contains(line, "npm ci --prefix extensions/sdk-ts") {
					oracle = true
					if !verified {
						t.Errorf("%s prepares dependencies before installing and checking pinned npm", name)
					}
				}
			}
		}
		if !oracle {
			t.Errorf("%s has no pinned oracle preparation", name)
		}
	}
}
