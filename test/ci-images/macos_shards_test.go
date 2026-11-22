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

// macosUnitSeconds is what go test itself takes (package ok seconds, rounded up) for each unit in full run 44985297151 on 2026-11-22 (macos-15).
// macosJobSetupSeconds is the maximum across the six grouped jobs of (measured job seconds - actual Rust warm seconds - sum of unit ok seconds): 249, 230, 303, 229, 290, 296 s -> 303 s.
// macosRustWarmSeconds is the maximum across the run's seven warmed targets (cli 37, 53, 60, 80 s; subprocess 60, 66 s; env-oracle 56 s) -> 80 s.
// Cold caches add about 200 to 255 s more (sbcl and pi-oracle misses), measured in the earlier runs 44690207820 and of 2026-11-14 and not measured in run 44985297151; with the highest planned job at 1408 s that is 1663 s = 69.3% of the 40-minute limit, under 70%.
// native is not a unit of macos-units.sh: its job is the smoke run (1223 s in run 44985297151).
var macosUnitSeconds = map[string]int{
	"cli-1": 483, "cli-2": 367, "cli-3": 355, "cli-4": 379, "cli-5": 436, "cli-6": 245, "cli-7": 341, "cli-8": 234,
	"extension-host-1": 346, "extension-host-2": 272, "extension-host-3": 344, "extension-host-4": 320, "extension-host-5": 361,
	"packages": 148,
}

// macosMeasuredJobSeconds records the whole-job wall seconds of each grouped macOS job in run 44985297151 (2026-11-22).
var macosMeasuredJobSeconds = map[string]int{
	"cli-1 cli-2": 1159,
	"cli-3 cli-4": 1001,
	"cli-5 cli-6": 1064,
	"cli-7 cli-8": 857,
	"extension-host-1 extension-host-2 packages":         1172,
	"extension-host-3 extension-host-4 extension-host-5": 1387,
}

const (
	macosJobSetupSeconds = 303
	// macosRustWarmSeconds is the maximum measured cold-build time for one Rust Cargo target (cli 37, 53, 60, 80 s;
	// subprocess 60, 66 s; env-oracle 56 s) in run 44985297151 (2026-11-22).
	macosRustWarmSeconds = 80
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
		if measured, ok := macosMeasuredJobSeconds[entry]; ok && total < measured {
			t.Errorf("macos job %q planned %d s is below its measured %d s in run 44985297151: update macosJobSetupSeconds or macosUnitSeconds", entry, total, measured)
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
