// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestLightPRChangedRunAlwaysIncludesTreeWideGuardPackages proves that when ci-test-changed runs
// test-grouped.sh in changed mode with TEST_CHANGED_FOR=fast-parallel, the three tree-wide
// invariant guard packages (./internal/testenv, ./test/ci-images, ./test/docs-drift) always run
// even when changed-packages.py reports only an unrelated package.
func TestLightPRChangedRunAlwaysIncludesTreeWideGuardPackages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture fake go is a bash script found through PATH")
	}
	root := groupedTestFixture(t)
	copyCIFixture(t, root, "automation/ci/test-json-report.py")
	// Fake changed-packages.py reports only ./alpha as affected (none of the guard packages).
	writeCIFixture(t, root, "automation/ci/changed-packages.py", "print(\"direct ./alpha\")\nprint(\"affected ./alpha\")\n")
	// Extend the fixture fake go to list the three guard packages as part of ./....
	fakeGoPath := filepath.Join(root, "bin", "go")
	fakeGoBytes, err := os.ReadFile(fakeGoPath)
	if err != nil {
		t.Fatal(err)
	}
	fakeGo := strings.Replace(
		string(fakeGoBytes),
		"printf '%s\\n' ./alpha ./beta ./coding/cli ./coding/extension/host/subprocess ./test/extension-conformance",
		"printf '%s\\n' ./alpha ./beta ./internal/testenv ./test/ci-images ./test/docs-drift ./coding/cli ./coding/extension/host/subprocess ./test/extension-conformance",
		1,
	)
	if fakeGo == string(fakeGoBytes) {
		t.Fatal("could not patch fixture fake go list output")
	}
	if err := os.WriteFile(fakeGoPath, []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}
	pkgLog := filepath.Join(root, "packages")
	writeCIFixture(t, root, "packages", "")
	cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/test-grouped.sh"), "changed")
	cmd.Dir = root
	cmd.Env = append(
		os.Environ(),
		"PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TMPDIR="+t.TempDir(),
		"TEST_LOG="+pkgLog,
		"TEST_BASE=HEAD",
		"TEST_CHANGED_FOR=fast-parallel",
		"TEST_CHANGED_RACE=none",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("test-grouped.sh changed (TEST_CHANGED_FOR=fast-parallel): %v\n%s", err, out)
	}
	loggedBytes, err := os.ReadFile(pkgLog)
	if err != nil {
		t.Fatal(err)
	}
	ran := strings.Fields(string(loggedBytes))
	for _, want := range []string{"./alpha", "./internal/testenv", "./test/ci-images", "./test/docs-drift"} {
		if !slices.Contains(ran, want) {
			t.Errorf("light PR changed run did not run %s; ran = %v", want, ran)
		}
	}
	if slices.Contains(ran, "./beta") {
		t.Errorf("light PR changed run unexpectedly ran unaffected ./beta; ran = %v", ran)
	}
}

// TestPortmapShardsAreQueueOnlyAndPromoteOnPortmapEdits verifies that all 12 portmap shards stay in
// tier queue (off ordinary light PR runs) and that editing docs/parity/PORT_MAP.md or
// test/parity/cmd/portmapcheck/** promotes the PR via classify-changes.sh.
func TestPortmapShardsAreQueueOnlyAndPromoteOnPortmapEdits(t *testing.T) {
	light := planWith(t, "pull_request", "label", "false")
	if light.Full || !light.FullRequired {
		t.Fatalf("label mode light plan: full=%v full-required=%v, want false/true", light.Full, light.FullRequired)
	}
	lightShards := light.shards("linux")
	for _, row := range tierRows(t) {
		if row.job == "linux" && strings.HasPrefix(row.shard, "portmap-") {
			if row.tier != "queue" {
				t.Errorf("ci-tiers.txt: %s has tier %q, want queue", row.shard, row.tier)
			}
			if slices.Contains(lightShards, row.shard) {
				t.Errorf("light PR plan unexpectedly includes %s", row.shard)
			}
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := t.TempDir()
	runGitIn(t, repo, "init", "-q")
	write := func(rel, content string) {
		full := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "base\n")
	runGitIn(t, repo, "add", "-A")
	runGitIn(t, repo, "commit", "-q", "-m", "base")
	base := runGitIn(t, repo, "rev-parse", "HEAD")

	script := filepath.Join(repoRoot(t), "automation", "ci", "classify-changes.sh")
	for _, path := range []string{"docs/parity/PORT_MAP.md", "test/parity/cmd/portmapcheck/main.go"} {
		t.Run(path, func(t *testing.T) {
			runGitIn(t, repo, "checkout", "-q", "--detach", base)
			write(path, "changed\n")
			runGitIn(t, repo, "add", "-A")
			runGitIn(t, repo, "commit", "-q", "-m", path)
			head := runGitIn(t, repo, "rev-parse", "HEAD")
			cmd := testenv.ScriptCommand(t, script, "pull_request", base, head)
			cmd.Dir = repo
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("classify-changes.sh: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), "os-specific=true") {
				t.Errorf("editing %s did not promote to full run:\n%s", path, out)
			}
		})
	}
}

// TestNightlyWindowsConcurrencyStressJobWired checks that nightly.yml defines the dedicated
// windows-concurrency-stress job on windows-latest, installs the cached pinned Pi oracle before
// running the stress suite (needed by pilock and auth-storage cross-process Pi interop tests),
// and invokes automation/ci/windows-concurrency-stress.sh.
func TestNightlyWindowsConcurrencyStressJobWired(t *testing.T) {
	nightly := readWorkflow(t, "nightly.yml")
	job, ok := nightly.Jobs["windows-concurrency-stress"]
	if !ok {
		t.Fatal("nightly.yml has no windows-concurrency-stress job")
	}
	if job.TimeoutMinutes < 30 || job.TimeoutMinutes > 90 {
		t.Errorf("windows-concurrency-stress timeout-minutes = %d, want 30..90", job.TimeoutMinutes)
	}
	var (
		hasBuildStore  bool
		hasOracleCache bool
		oracleIdx      = -1
		stressIdx      = -1
	)
	for i, step := range job.Steps {
		if strings.Contains(step.Run, "PIG_PI_BUILD_STORE") {
			hasBuildStore = true
		}
		if strings.HasPrefix(step.Uses, "actions/cache/restore@") && strings.Contains(step.Name, "Pi oracle") {
			hasOracleCache = true
		}
		if strings.Contains(step.Run, "automation/pin/pin.py install-oracle") {
			oracleIdx = i
			if !strings.Contains(step.Run, "automation/ci/install-npm-toolchain.sh") {
				t.Error("windows-concurrency-stress installs Pi oracle without installing pinned npm toolchain first")
			}
		}
		if strings.Contains(step.Run, "automation/ci/windows-concurrency-stress.sh") {
			stressIdx = i
		}
	}
	if !hasBuildStore {
		t.Error("windows-concurrency-stress job does not set PIG_PI_BUILD_STORE under RUNNER_TEMP")
	}
	if !hasOracleCache {
		t.Error("windows-concurrency-stress job does not restore the cached Pi oracle build")
	}
	if oracleIdx < 0 {
		t.Error("windows-concurrency-stress job does not install the pinned Pi oracle")
	}
	if stressIdx < 0 {
		t.Error("windows-concurrency-stress job does not invoke automation/ci/windows-concurrency-stress.sh")
	}
	if oracleIdx >= 0 && stressIdx >= 0 && oracleIdx > stressIdx {
		t.Errorf("windows-concurrency-stress installs Pi oracle at step %d after running stress script at step %d", oracleIdx, stressIdx)
	}
}

// TestScheduledUnchangedMainSkipWiring checks that ci.yml and nightly.yml wire the unchanged-main
// schedule gate cleanly: only schedule runs may skip, workflow_dispatch/PR/merge_group/push never
// skip, ci.yml preserves the 5-day cache refresh cadence, and nightly.yml skips only deterministic
// jobs while keeping stochastic jobs (race, fuzz, windows-concurrency-stress) unconditional.
func TestScheduledUnchangedMainSkipWiring(t *testing.T) {
	ci := readWorkflow(t, "ci.yml")
	changes, ok := ci.Jobs["changes"]
	if !ok {
		t.Fatal("ci.yml has no changes job")
	}
	foundCICheck := false
	for _, step := range changes.Steps {
		if strings.Contains(step.Run, "automation/ci/check-unchanged-main.py ci") {
			foundCICheck = true
			if !strings.Contains(step.Run, "--cache-refresh-days 5") {
				t.Errorf("ci.yml unchanged-main check does not pass --cache-refresh-days 5:\n%s", step.Run)
			}
		}
	}
	if !foundCICheck {
		t.Error("ci.yml changes job does not invoke automation/ci/check-unchanged-main.py ci")
	}

	// Verify that Linux verification (verify) accepts skipped Linux shards ONLY on schedule with SKIP_UNCHANGED=true
	// (or docs-only PRODUCT=false), and fails closed for workflow_dispatch / merge_group / pull_request.
	verify := ci.Jobs["verify"]
	var verifyScript string
	for _, step := range verify.Steps {
		if step.Env["RESULT"] == "${{ needs.linux.result }}" {
			verifyScript = step.Run
		}
	}
	if verifyScript == "" {
		t.Fatal("ci.yml verify job has no Linux shard check step")
	}
	for _, tc := range []struct {
		name  string
		event string
		prod  string
		skip  string
		res   string
		pass  bool
	}{
		{"schedule unchanged skip passes", "schedule", "true", "true", "skipped", true},
		{"schedule non-skip with skipped linux fails", "schedule", "true", "false", "skipped", false},
		{"workflow_dispatch cannot skip via SKIP_UNCHANGED", "workflow_dispatch", "true", "true", "skipped", false},
		{"merge_group cannot skip via SKIP_UNCHANGED", "merge_group", "true", "true", "skipped", false},
		{"pull_request cannot skip via SKIP_UNCHANGED", "pull_request", "true", "true", "skipped", false},
		{"docs-only skip passes", "pull_request", "false", "false", "skipped", true},
	} {
		t.Run("verify/"+tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), testenv.Bash(t), "-euo", "pipefail", "-c", verifyScript)
			cmd.Env = append(os.Environ(),
				"EVENT="+tc.event,
				"CHANGES=success",
				"PRODUCT="+tc.prod,
				"SKIP_UNCHANGED="+tc.skip,
				"RESULT="+tc.res,
			)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.pass {
				t.Fatalf("pass = %v, want %v\n%s", err == nil, tc.pass, out)
			}
		})
	}

	// Check nightly.yml head-state gate: deterministic jobs depend on head-state; stochastic jobs do not.
	nightly := readWorkflow(t, "nightly.yml")
	headState, ok := nightly.Jobs["head-state"]
	if !ok {
		t.Fatal("nightly.yml has no head-state job")
	}
	foundNightlyCheck := false
	for _, step := range headState.Steps {
		if strings.Contains(step.Run, "automation/ci/check-unchanged-main.py nightly") {
			foundNightlyCheck = true
		}
	}
	if !foundNightlyCheck {
		t.Error("nightly.yml head-state job does not invoke automation/ci/check-unchanged-main.py nightly")
	}
	for _, det := range []string{"oracle-sweeps", "pig-bolt-compiler", "allocations", "strip-matrix", "strip-tests-sets"} {
		job, ok := nightly.Jobs[det]
		if !ok {
			t.Errorf("nightly.yml missing deterministic job %s", det)
			continue
		}
		if !slices.Contains(needsOf(job.Needs), "head-state") {
			t.Errorf("nightly.yml deterministic job %s does not need head-state", det)
		}
		if !strings.Contains(job.If, "needs.head-state.outputs.run-deterministic == 'true'") {
			t.Errorf("nightly.yml deterministic job %s if = %q, want run-deterministic guard", det, job.If)
		}
	}
	for _, stoch := range []string{"race", "fuzz", "windows-concurrency-stress"} {
		job, ok := nightly.Jobs[stoch]
		if !ok {
			t.Errorf("nightly.yml missing stochastic job %s", stoch)
			continue
		}
		if slices.Contains(needsOf(job.Needs), "head-state") || strings.Contains(job.If, "run-deterministic") {
			t.Errorf("nightly.yml stochastic job %s must not be gated by head-state (needs=%v if=%q)", stoch, job.Needs, job.If)
		}
	}
}
