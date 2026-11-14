// SPDX-License-Identifier: MIT

package ciimages

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// macOS runs the packages whose tests reached macOS-only failures (104-byte
// socket paths, /var -> /private/var temporary directories, small PTY buffers,
// fsnotify gaps) next to the native smoke run: every cmd/pig shard that make
// test-cli runs on Linux, the extension host in three parts, and the other
// packages. GitHub runs at most five macOS jobs at a time, so the units share
// at most five jobs; each unit still runs in its own go test process.
func TestMacOSShardsTestTheMacOSSensitivePackages(t *testing.T) {
	job, ok := verificationJobs(t)["macos"]
	if !ok {
		t.Fatal("ci.yml has no macos job")
	}
	shards, cliPkg := cliShards(t)
	wantUnits := []string{"native"}
	for shard := 1; shard <= shards; shard++ {
		wantUnits = append(wantUnits, "cli-"+strconv.Itoa(shard))
	}
	wantUnits = append(wantUnits, "extension-host-1", "extension-host-2", "extension-host-3", "packages")
	var gotUnits []string
	for _, entry := range planFor(t, "merge_group", "").shards("macos") {
		units := strings.Fields(entry)
		if slices.Contains(units, "native") && len(units) != 1 {
			t.Errorf("macos job %q groups native with other units; native has its own setup", entry)
		}
		gotUnits = append(gotUnits, units...)
	}
	slices.Sort(gotUnits)
	slices.Sort(wantUnits)
	if !slices.Equal(gotUnits, wantUnits) {
		t.Fatalf("macos units = %v, want each of %v exactly once", gotUnits, wantUnits)
	}
	if n := len(planFor(t, "merge_group", "").shards("macos")); n > 5 {
		t.Errorf("macos has %d jobs; GitHub runs at most 5 macOS jobs at a time", n)
	}
	if job.Strategy.FailFast == nil || *job.Strategy.FailFast {
		t.Error("macos shards must all run when one fails")
	}
	if job.Timeout <= 0 || job.Timeout > 40 {
		t.Errorf("macos timeout = %d minutes, want 1 to 40", job.Timeout)
	}
	steps := map[string]bool{}
	for _, step := range job.Steps {
		switch {
		case strings.HasPrefix(step.Uses, "actions/setup-python@") && step.With["python-version"] == "3.12":
			steps["python"] = true
		case step.If == "matrix.shard != 'native'" && strings.Contains(step.Run, `rustup default "$RUST_VERSION"`):
			steps["rust"] = true
		case step.If == "matrix.shard == 'native'" && strings.Contains(step.Run, "go test -timeout 30m ./ai ./internal/codingagent/tools ./internal/nativeplatform ./tui\n"):
			steps["native"] = true
		case strings.Contains(step.If, "contains(matrix.shard, 'cli-')") && strings.TrimSpace(step.Run) == "automation/ci/macos-units.sh cli" && step.Env["UNITS"] == "${{ matrix.shard }}":
			steps["cli"] = true
		case strings.Contains(step.If, "contains(matrix.shard, 'extension-host-')") && strings.TrimSpace(step.Run) == "automation/ci/macos-units.sh extension-host" && step.Env["UNITS"] == "${{ matrix.shard }}":
			steps["host"] = true
		case strings.Contains(step.If, "contains(matrix.shard, 'packages')") && strings.TrimSpace(step.Run) == "automation/ci/macos-units.sh packages" && step.Env["UNITS"] == "${{ matrix.shard }}":
			steps["packages"] = true
		}
	}
	// The shards run tests that build Rust extensions and drive Python harnesses, so they need the toolchains Linux and Windows pin.
	for _, name := range []string{"native", "cli", "host", "packages", "python", "rust"} {
		if !steps[name] {
			t.Errorf("no macos step provides %s as declared", name)
		}
	}
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "automation/ci/macos-units.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	cliRun := regexp.MustCompile(`test-shard-pattern\.sh "\$\{unit#cli-\}" ([0-9]+) ` + regexp.QuoteMeta(cliPkg) + `\); then\n\s*go test -timeout 30m -json -run "\$run" ` + regexp.QuoteMeta(cliPkg) + ` \| python3 automation/ci/test-json-report\.py \|\| failed=1\n`)
	if match := cliRun.FindStringSubmatch(script); match == nil {
		t.Error("macos-units.sh does not run a cmd/pig unit as its own go test of one test-shard-pattern.sh shard")
	} else if match[1] != strconv.Itoa(shards) {
		t.Errorf("macos-units.sh splits cmd/pig into %s shards, want %d as make test-cli runs", match[1], shards)
	}
	if !regexp.MustCompile(`test-shard-pattern\.sh "\$\{unit##\*-\}" 3 \./coding/extension/host/subprocess\); then\n\s*go test -timeout 30m -json -run "\$run" \./coding/extension/host/subprocess \| python3 automation/ci/test-json-report\.py \|\| failed=1\n`).MatchString(script) {
		t.Error("macos-units.sh does not run an extension host unit as its own go test of one third")
	}
	if !strings.Contains(script, "go test -timeout 30m -json ./env ./coding/piglet | python3 automation/ci/test-json-report.py || failed=1\n") {
		t.Error("macos-units.sh does not run the packages unit")
	}
}

// macosUnitsFixture is groupedTestFixture with macos-units.sh and the report it pipes go test into. The fixture's python3 stands for the interpreter: it fails unless the report script it is given exists, reads the events and exits 0, as the report does.
func macosUnitsFixture(t *testing.T) string {
	t.Helper()
	root := groupedTestFixture(t)
	copyCIFixture(t, root, "automation/ci/macos-units.sh")
	copyCIFixture(t, root, "automation/ci/test-json-report.py")
	writeCIFixture(t, root, "bin/python3", "#!/usr/bin/env bash\ntest -f \"$1\" || exit 97\ncat >/dev/null\n")
	writeCIFixture(t, root, "runs", "")
	writeCIFixture(t, root, "packages", "")
	t.Setenv("RUN_LOG", filepath.Join(root, "runs"))
	t.Setenv("TEST_LOG", filepath.Join(root, "packages"))
	// The fake go test runs only with the fixtures test-grouped.sh exports; macos-units.sh needs none.
	t.Setenv("CI_TEST_FIXTURES", "ready")
	t.Setenv("RUNNER_TEMP", t.TempDir())
	return root
}

// A unit's go test runs in a pipeline whose last command, the report, exits 0: the unit, and with it the step, must still fail when go test fails, and the later units of the job must still run.
func TestMacOSUnitFailsWhenItsGoTestFails(t *testing.T) {
	_, cliPkg := cliShards(t)
	for kind, tc := range map[string]struct{ units, pkg string }{
		"cli":            {"cli-1 cli-2", cliPkg},
		"extension-host": {"extension-host-1 extension-host-2", "./coding/extension/host/subprocess"},
		"packages":       {"packages", "./env"},
	} {
		t.Run(kind, func(t *testing.T) {
			root := macosUnitsFixture(t)
			first := strings.Fields(tc.units)[0]
			for _, fail := range []bool{false, true} {
				cmd := exec.CommandContext(t.Context(), testenv.Bash(t), "automation/ci/macos-units.sh", kind)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "UNITS="+tc.units)
				if fail {
					cmd.Env = append(cmd.Env, "FAIL_PACKAGE="+tc.pkg)
				}
				output, err := cmd.CombinedOutput()
				if !fail {
					if err != nil {
						t.Fatalf("%s: %v\n%s", tc.units, err, output)
					}
					continue
				}
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), "::error::macOS unit "+first+" failed") {
					t.Fatalf("%s with a failing go test: %v, want exit status 1 and the unit's error\n%s", tc.units, err, output)
				}
				for unit := range strings.FieldsSeq(tc.units) {
					if !strings.Contains(string(output), "::group::"+unit+"\n") {
						t.Errorf("%s did not run after a failing unit\n%s", unit, output)
					}
				}
			}
		})
	}
}

// macosUnitSteps returns, for each kind of unit that macos-units.sh runs, the text a job's shard name must hold for the workflow to run the step of that kind.
func macosUnitSteps(t *testing.T) map[string]string {
	t.Helper()
	condition := regexp.MustCompile(`contains\(matrix\.shard, '([^']+)'\)`)
	steps := map[string]string{}
	for _, step := range verificationJobs(t)["macos"].Steps {
		kind, ok := strings.CutPrefix(strings.TrimSpace(step.Run), "automation/ci/macos-units.sh ")
		if !ok {
			continue
		}
		match := condition.FindAllStringSubmatch(step.If, -1)
		if len(match) != 1 || step.Env["UNITS"] != "${{ matrix.shard }}" {
			t.Fatalf("macos step %q runs if %q with UNITS %q, want one contains(matrix.shard, ...) condition and the job's units", step.Run, step.If, step.Env["UNITS"])
		}
		steps[kind] = match[0][1]
	}
	return steps
}

// However ci-tiers.txt packs the units into jobs, the macOS jobs of the full run together run every test once: each job runs the macos-units.sh steps its shard name selects in the workflow, the cmd/pig units
// and the extension host units select every test of their package exactly once across the jobs, and the packages unit runs once. The fixture's fake go lists the same tests for every package and records each go test.
func TestMacOSJobsRunEveryTestOfEveryUnitExactlyOnce(t *testing.T) {
	shards, cliPkg := cliShards(t)
	const hostPkg = "./coding/extension/host/subprocess"
	steps := macosUnitSteps(t)
	root := macosUnitsFixture(t)
	runs, packages := filepath.Join(root, "runs"), filepath.Join(root, "packages")
	for _, entry := range planFor(t, "merge_group", "").shards("macos") {
		for kind, marker := range steps {
			if !strings.Contains(entry, marker) {
				continue
			}
			cmd := exec.CommandContext(t.Context(), testenv.Bash(t), "automation/ci/macos-units.sh", kind)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "UNITS="+entry)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("macos job %q, %s units: %v\n%s", entry, kind, err, output)
			}
		}
	}
	data, err := os.ReadFile(runs)
	if err != nil {
		t.Fatal(err)
	}
	patterns := map[string][]string{}
	for line := range strings.Lines(string(data)) {
		pkg, pattern, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			t.Fatalf("a unit ran %q, want one package with a -run pattern", line)
		}
		patterns[pkg] = append(patterns[pkg], pattern)
	}
	if len(patterns) != 2 || len(patterns[cliPkg]) != shards || len(patterns[hostPkg]) != 3 {
		t.Fatalf("the macos jobs ran %d shards of %s and %d of %s (sharded packages: %v), want %d and 3 of those two", len(patterns[cliPkg]), cliPkg, len(patterns[hostPkg]), hostPkg, slices.Sorted(maps.Keys(patterns)), shards)
	}
	requireShardPartition(t, patterns[cliPkg])
	requireShardPartition(t, patterns[hostPkg])
	data, err = os.ReadFile(packages)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for line := range strings.Lines(string(data)) {
		got[strings.TrimSpace(line)]++
	}
	if want := map[string]int{cliPkg: shards, hostPkg: 3, "./env": 1, "./coding/piglet": 1}; !maps.Equal(got, want) {
		t.Fatalf("go test processes per package = %v, want %v", got, want)
	}
}

// macosUnitSeconds is what go test itself took for each unit: the longest of its times in runs 43592372203, 43594615087, 43599788980 and 43599786195 (pull requests of 2026-11-14 on macos-15). extension-host-3 took 1323 s in run
// 43302835411 (2026-11-12), where these units were first measured; it takes 408 to 445 s now. The three units of "macOS cli-2 cli-8 extension-host-2" took 903, 517 and (cancelled) more than 760 s in run 43599786195, 1.8 to 3 times
// their other runs (451 to 513, 166 to 198, 287 to 332): a slow runner, left out here. macosJobSetupSeconds is what a job spent besides its units, the checkout, the toolchains, the oracle and compiling: 267 to 272 s in the same runs.
// Every unit prints its ten slowest tests and the time of each package, so a later full run gives the numbers to replace these with. native is not a unit of macos-units.sh: its job is the smoke run, 13 minutes whole in run 39510928254.
var macosUnitSeconds = map[string]int{
	"cli-1": 507, "cli-2": 513, "cli-3": 498, "cli-4": 460, "cli-5": 276, "cli-6": 217, "cli-7": 229, "cli-8": 198,
	"extension-host-1": 470, "extension-host-2": 332, "extension-host-3": 445,
	"packages": 163,
}

const macosJobSetupSeconds = 300

// A macOS job that reaches its limit is cancelled with no failing test, and the run is red for a reason no log names: "macOS cli-1 extension-host-3 packages" held 2086 s of tests under a 40-minute limit when extension-host-3 took 1323 s.
// So at the measured times every job of the plan keeps a quarter of its limit free. A slow runner is not covered: the cancelled job of run 43599786195 ran its units 1.8 to 3 times slower than the other runs did, and no packing keeps a job
// under the limit at that factor; this test keeps the plan from being packed past the times it was measured at.
func TestMacOSJobsKeepAQuarterOfTheirLimit(t *testing.T) {
	limit := verificationJobs(t)["macos"].Timeout * 60
	for _, entry := range planFor(t, "merge_group", "").shards("macos") {
		if entry == "native" {
			continue
		}
		total := macosJobSetupSeconds
		for unit := range strings.FieldsSeq(entry) {
			seconds, ok := macosUnitSeconds[unit]
			if !ok {
				t.Errorf("macos job %q holds the unit %s, which has no measured time", entry, unit)
			}
			total += seconds
		}
		if total*4 > limit*3 {
			t.Errorf("macos job %q takes %d s at the measured times (%d s of them setup), more than three quarters of its %d s limit: move a unit to another job in automation/ci/ci-tiers.txt", entry, total, macosJobSetupSeconds, limit)
		}
	}
}

// The macOS shards run the Pi oracle and the Node extensions under the node on PATH. A step that prepends the whole Homebrew bin directory puts the image's default node (22.x) ahead of the pinned one; Node 22 then warns about an empty NO_COLOR under FORCE_COLOR and the oracle's stderr differs. The toolchain identity check must run after the last PATH change and before any test.
func TestMacOSShardsTestUnderThePinnedToolchain(t *testing.T) {
	job, ok := verificationJobs(t)["macos"]
	if !ok {
		t.Fatal("ci.yml has no macos job")
	}
	lastPathChange, check, firstTest := -1, -1, -1
	for i, step := range job.Steps {
		if strings.Contains(step.Run, "$(brew --prefix)/bin") {
			t.Errorf("macos step %d puts the whole Homebrew bin directory on PATH:\n%s", i, step.Run)
		}
		if strings.Contains(step.Run, "GITHUB_PATH") {
			lastPathChange = i
		}
		if step.If == "" && strings.Contains(step.Run, `test "$(node --version)" = "v$(cat .node-version)"`) && strings.Contains(step.Run, `test "$(npm --version)" = "$NPM_VERSION"`) {
			check = i
		}
		if firstTest < 0 && strings.Contains(step.Run, "go test ") {
			firstTest = i
		}
	}
	if check < 0 || check < lastPathChange || firstTest < 0 || check > firstTest {
		t.Fatalf("macos toolchain check at step %d, last PATH change at step %d, first test at step %d: the check must follow every PATH change and precede every test", check, lastPathChange, firstTest)
	}
}
