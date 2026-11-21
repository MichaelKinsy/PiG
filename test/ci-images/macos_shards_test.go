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
// test-cli runs on Linux, every extension host shard that make test-subprocess
// runs on Linux, and the other
// packages. GitHub runs at most five macOS jobs at a time, so the units are
// packed into at most seven jobs (native plus six grouped jobs) so every job
// stays within 60% of the 40-minute limit on warm caches; each unit still runs
// in its own go test process.
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
	wantUnits = append(wantUnits, hostUnits(t)...)
	wantUnits = append(wantUnits, "packages")
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
	if n := len(planFor(t, "merge_group", "").shards("macos")); n > 7 {
		t.Errorf("macos has %d jobs; want at most 7 (native plus six grouped jobs)", n)
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
	hostCount, hostPkg := hostShards(t)
	hostRun := regexp.MustCompile(`test-shard-pattern\.sh "\$\{unit##\*-\}" ([0-9]+) ` + regexp.QuoteMeta(hostPkg) + `\); then\n\s*go test -timeout 30m -json -run "\$run" ` + regexp.QuoteMeta(hostPkg) + ` \| python3 automation/ci/test-json-report\.py \|\| failed=1\n`)
	if match := hostRun.FindStringSubmatch(script); match == nil {
		t.Error("macos-units.sh does not run an extension host unit as its own go test of one test-shard-pattern.sh shard")
	} else if match[1] != strconv.Itoa(hostCount) {
		t.Errorf("macos-units.sh splits %s into %s shards, want %d as make test-subprocess runs", hostPkg, match[1], hostCount)
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
	hostCount, hostPkg := hostShards(t)
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
	// The packages unit also runs the plugins watcher's tests of ./chord/nextnode/plugins (kqueue and birthtime are macOS-only), one -run pattern, not a shard of the package.
	const watcherPkg = "./chord/nextnode/plugins"
	if len(patterns) != 3 || len(patterns[cliPkg]) != shards || len(patterns[hostPkg]) != hostCount || len(patterns[watcherPkg]) != 1 {
		t.Fatalf("the macos jobs ran %d shards of %s, %d of %s and %d patterns of %s (packages with a -run: %v), want %d, %d and 1", len(patterns[cliPkg]), cliPkg, len(patterns[hostPkg]), hostPkg, len(patterns[watcherPkg]), watcherPkg, slices.Sorted(maps.Keys(patterns)), shards, hostCount)
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
	if want := map[string]int{cliPkg: shards, hostPkg: hostCount, watcherPkg: 1, "./env": 1, "./coding/piglet": 1}; !maps.Equal(got, want) {
		t.Fatalf("go test processes per package = %v, want %v", got, want)
	}
}

// macosUnitSeconds is what go test itself takes for each unit on a normal-speed macos-15 runner.
// extension-host-1 to extension-host-5 and packages are measured from runs 44864086404 (release/v0.8.0) and 44870314130 (canary/win-oracle-080) of 2026-11-21,
// excluding the slow runner of 44870314130 job 136751895303 (2.1 to 2.8x slower across every step).
// cli-1 to cli-8 are modelled for the repacked pins in automation/ci/test-shard-weights.txt from the two runs' normal-speed coding/cli total (3845 s across the eight
// shards, fitted against automation/ci/test-shard-timings/cli.json as 1.79*portable_linux_load + 164 s first-unit Cargo build, split evenly across each pair) and will be
// replaced by the next full macOS run's report. The pre-test warm step (macosRustWarmSeconds below) is expected to remove the 164 s first-unit build, and the model will
// follow the first macOS run's measurement.
// macosJobSetupSeconds is the warm-cache setup and compile overhead (190 to 272 s measured on main run 44690207820 and
// 2026-11-14 runs); cold-cache runs add about 200 to 255 s more (sbcl and pi-oracle misses), which stays under 70% of the 40-minute limit when warm jobs stay at or under 60%.
// native is not a unit of macos-units.sh: its job is the smoke run (15 minutes warm, 19 to 20 minutes cold).
var macosUnitSeconds = map[string]int{
	"cli-1": 464, "cli-2": 517, "cli-3": 516, "cli-4": 516, "cli-5": 519, "cli-6": 411, "cli-7": 517, "cli-8": 493,
	"extension-host-1": 364, "extension-host-2": 379, "extension-host-3": 279, "extension-host-4": 343, "extension-host-5": 274,
	"packages": 123,
}

const (
	macosJobSetupSeconds = 300
	// macosRustWarmSeconds is the cold-build budget for one Rust Cargo target (cli, subprocess, or env-oracle) warmed by
	// automation/ci/test-warm.sh --rust-only before go test starts (default Cargo parallelism, not CARGO_BUILD_JOBS=1).
	macosRustWarmSeconds = 60
)

func macosRustWarmTargets(entry string) []string {
	var targets []string
	padded := " " + entry + " "
	if strings.Contains(padded, " cli-") {
		targets = append(targets, "cli")
	}
	if strings.Contains(padded, " extension-host-") {
		targets = append(targets, "subprocess")
	}
	if strings.Contains(padded, " packages ") {
		targets = append(targets, "env-oracle")
	}
	return targets
}

// A macOS job that reaches its 40-minute limit is cancelled with no failing test (run 44864086404 cli-4 cli-7 extension-host-1; run 44870314130 cli-1 extension-host-3 packages).
// So at warm-cache times (including the cold Rust SDK warm-up step, since macOS keeps no Cargo cache in 0.8.0) every grouped job of the plan stays at or under 60% of its limit
// (1440 s of 2400 s), leaving 40% headroom (1.4x slow-pool factor plus cold sbcl/pi-oracle misses) inside the 40-minute limit.
func TestMacOSJobsKeepAQuarterOfTheirLimit(t *testing.T) {
	limit := verificationJobs(t)["macos"].Timeout * 60
	for _, entry := range planFor(t, "merge_group", "").shards("macos") {
		if entry == "native" {
			continue
		}
		warm := len(macosRustWarmTargets(entry)) * macosRustWarmSeconds
		total := macosJobSetupSeconds + warm
		for unit := range strings.FieldsSeq(entry) {
			seconds, ok := macosUnitSeconds[unit]
			if !ok {
				t.Errorf("macos job %q holds the unit %s, which has no measured time", entry, unit)
			}
			total += seconds
		}
		if total*5 > limit*3 {
			t.Errorf("macos job %q takes %d s at warm-cache times (%d s setup + %d s Rust warm-up), more than 60%% of its %d s limit: move a unit to another job in automation/ci/ci-tiers.txt", entry, total, macosJobSetupSeconds, warm, limit)
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

// Every non-native macOS job runs tests that build Rust packed cells with a 120 s wait budget (coding/cli, coding/extension/host/subprocess) or the Rust Pi env daemon (env).
// Without a pre-warmed SharedCargoTargetDir, whichever 120 s test runs first in a job compiles pig-sdk and serde at CARGO_BUILD_JOBS=1 cold and gets killed at 120 s (run 44896736816:
// macOS cli-5 cli-6 and macOS cli-7 cli-8). Every planned macOS job that holds a cli-, extension-host-, or packages unit must run automation/ci/test-warm.sh --rust-only in its own
// workflow step (outside any go test budget) before any macos-units.sh step, warming each target its units use.
func TestMacOSJobsWarmTheirRustCargoTargetsBeforeTests(t *testing.T) {
	job, ok := verificationJobs(t)["macos"]
	if !ok {
		t.Fatal("ci.yml has no macos job")
	}
	warmIdx, firstUnitsIdx := -1, -1
	var warmIf, warmRun, warmUnitsEnv string
	for i, step := range job.Steps {
		switch {
		case strings.Contains(step.Run, "automation/ci/test-warm.sh --rust-only"):
			if warmIdx >= 0 {
				t.Errorf("macos has multiple Rust warm steps (steps %d and %d)", warmIdx, i)
			}
			warmIdx = i
			warmIf = step.If
			warmRun = step.Run
			warmUnitsEnv = step.Env["UNITS"]
		case strings.Contains(step.Run, "automation/ci/macos-units.sh"):
			if firstUnitsIdx < 0 {
				firstUnitsIdx = i
			}
		}
	}
	if warmIdx < 0 {
		t.Fatal("macos job has no Rust Cargo target warm step (automation/ci/test-warm.sh --rust-only)")
	}
	if firstUnitsIdx < 0 {
		t.Fatal("macos job has no macos-units.sh step")
	}
	if warmIdx >= firstUnitsIdx {
		t.Fatalf("macos Rust warm step at %d must precede the first macos-units.sh step at %d", warmIdx, firstUnitsIdx)
	}
	if warmUnitsEnv != "${{ matrix.shard }}" {
		t.Errorf("macos Rust warm step UNITS = %q, want ${{ matrix.shard }}", warmUnitsEnv)
	}
	for _, entry := range planFor(t, "merge_group", "").shards("macos") {
		targets := macosRustWarmTargets(entry)
		if len(targets) == 0 {
			continue
		}
		if !macosWarmStepCoversShard(warmIf, entry) {
			t.Errorf("macos Rust warm step (if: %q) does not run for planned job %q", warmIf, entry)
		}
		for _, target := range targets {
			if !strings.Contains(warmRun, "targets+=("+target+")") {
				t.Errorf("macos Rust warm step does not warm the %s Cargo target needed by job %q:\n%s", target, entry, warmRun)
			}
		}
	}
}

// macosWarmStepCoversShard reports whether the workflow if-expression on the macOS warm step enables the step for shard.
func macosWarmStepCoversShard(expr, shard string) bool {
	if expr == "" {
		return true
	}
	if strings.Contains(expr, "matrix.shard != 'native'") && shard != "native" {
		// Reject an expression that also excludes this specific shard.
		if strings.Contains(expr, "matrix.shard != '"+shard+"'") {
			return false
		}
		return true
	}
	for _, marker := range []string{"cli-", "extension-host-", "packages"} {
		if strings.Contains(shard, marker) && strings.Contains(expr, "contains(matrix.shard, '"+marker+"')") {
			return true
		}
	}
	return false
}
