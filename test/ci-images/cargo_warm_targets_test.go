// SPDX-License-Identifier: MIT

package ciimages

import (
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	sharedCargoTargetCall = regexp.MustCompile(`testenv\.SharedCargoTargetDir\("([^"]+)"`)
	warmPackageLoop       = regexp.MustCompile(`for pkg in ([^;]+); do`)
	warmCargotargetCall   = regexp.MustCompile(`go run \./automation/ci/cargotarget (\S+)`)
)

// A package whose tests build Rust cells takes its Cargo target from testenv.SharedCargoTargetDir, so the SDK's
// dependencies compile once per machine. CI restores those targets from a cache and fills them with make test-warm before
// the tests run. A package that names a target there and is missing from test-warm.sh compiles its dependencies inside the
// go test deadline of its own run: coding/extension/host/runtimecell did, and a cold cargo build there outlasted the
// 10-minute package timeout (https://github.com/MichaelKinsy/PiG/actions/runs/40963676052).
func TestWarmScriptBuildsEverySharedCargoTarget(t *testing.T) {
	root := repoRoot(t)
	used, _ := sharedCargoTargetCallers(t, root)
	script, err := os.ReadFile(repoScript(t, "automation/ci/test-warm.sh"))
	if err != nil {
		t.Fatal(err)
	}
	warmed := map[string]bool{}
	loop := warmPackageLoop.FindSubmatch(script)
	if loop == nil {
		t.Fatal("automation/ci/test-warm.sh has no `for pkg in ...; do` loop over the packages that build Rust cells")
	}
	for pkg := range strings.FieldsSeq(string(loop[1])) {
		warmed[pkg] = true
	}
	for _, match := range warmCargotargetCall.FindAllSubmatch(script, -1) {
		if target := string(match[1]); target != `"$pkg"` {
			warmed[target] = true
		}
	}
	for _, pkg := range slices.Sorted(maps.Keys(used)) {
		if !warmed[pkg] {
			t.Errorf("%s takes the Cargo target %q from testenv.SharedCargoTargetDir, but automation/ci/test-warm.sh does not build it: CI would compile the Rust SDK dependencies cold inside that package's go test deadline", used[pkg], pkg)
		}
	}
	for _, pkg := range slices.Sorted(maps.Keys(warmed)) {
		if _, ok := used[pkg]; !ok {
			t.Errorf("automation/ci/test-warm.sh builds the Cargo target %q, which no test names in testenv.SharedCargoTargetDir", pkg)
		}
	}
}

var pinnedSharedCargoTargets = map[string]string{
	"cli":         "coding/cli",
	"conformance": "test/extension-conformance",
	"env-oracle":  "env",
	"runtimecell": "coding/extension/host/runtimecell",
	"subprocess":  "coding/extension/host/subprocess",
}

func sharedCargoTargetCallers(t *testing.T, root string) (files map[string]string, packages map[string]string) {
	t.Helper()
	files = map[string]string{}
	packages = map[string]string{}
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if file != root && (name == "testdata" || name == "node_modules" || name == "target" || name == "tmp" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		for _, match := range sharedCargoTargetCall.FindAllSubmatch(data, -1) {
			rel, relErr := filepath.Rel(root, file)
			if relErr != nil {
				return relErr
			}
			target := string(match[1])
			files[target] = filepath.ToSlash(rel)
			packages[target] = filepath.ToSlash(filepath.Dir(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no test calls testenv.SharedCargoTargetDir; the pattern in this test is stale")
	}
	return files, packages
}

// actions/cache saves a cache only when its key missed. The set of targets under the cached directory comes from
// test-warm.sh, so a key that hashed only the SDK lock would keep restoring the old set and never save a target the script
// gained.
func TestCargoCacheKeyHashesTheWarmScript(t *testing.T) {
	root := repoRoot(t)
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	keys := 0
	for line := range strings.SplitSeq(string(workflow), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "key: cargo-") {
			continue
		}
		keys++
		if !strings.Contains(trimmed, "automation/ci/test-warm.sh") {
			t.Errorf("the Cargo cache key %q does not hash automation/ci/test-warm.sh, so a target the script gains is never saved", trimmed)
		}
	}
	if keys == 0 {
		t.Fatal(".github/workflows/ci.yml has no `key: cargo-` line; the pattern in this test is stale")
	}
}

// Every macOS and Windows job that runs a package calling testenv.SharedCargoTargetDir must warm that target with
// automation/ci/test-warm.sh --rust-only in a workflow step before its first test step. The caller list is derived by
// walking the tree and pinned against pinnedSharedCargoTargets so a sixth caller fails this test immediately.
func TestMacOSAndWindowsJobsWarmEverySharedCargoTargetTheirPackagesCall(t *testing.T) {
	root := repoRoot(t)
	_, callers := sharedCargoTargetCallers(t, root)
	if !maps.Equal(callers, pinnedSharedCargoTargets) {
		t.Fatalf("SharedCargoTargetDir callers = %v, pinned = %v: update pinnedSharedCargoTargets and the macOS/Windows warm steps", callers, pinnedSharedCargoTargets)
	}
	pkgToTarget := map[string]string{}
	for target, pkg := range callers {
		pkgToTarget[pkg] = target
	}
	plan := planFor(t, "merge_group", "")
	jobs := verificationJobs(t)

	// macOS: map each planned shard to the packages it runs, then verify the warm step precedes tests and warms each needed target.
	macJob, ok := jobs["macos"]
	if !ok {
		t.Fatal("ci.yml has no macos job")
	}
	macWarmIdx, macFirstTestIdx := -1, -1
	var macWarmIf, macWarmRun, macWarmUnits string
	for i, step := range macJob.Steps {
		switch {
		case strings.Contains(step.Run, "automation/ci/test-warm.sh --rust-only"):
			if macWarmIdx >= 0 {
				t.Errorf("macos has multiple Rust warm steps (%d and %d)", macWarmIdx, i)
			}
			macWarmIdx, macWarmIf, macWarmRun, macWarmUnits = i, step.If, step.Run, step.Env["UNITS"]
		case strings.Contains(step.Run, "automation/ci/macos-units.sh"):
			if macFirstTestIdx < 0 {
				macFirstTestIdx = i
			}
		}
	}
	if macWarmIdx < 0 || macFirstTestIdx < 0 || macWarmIdx >= macFirstTestIdx {
		t.Fatalf("macos warm step (%d) must precede first macos-units.sh step (%d)", macWarmIdx, macFirstTestIdx)
	}
	if macWarmUnits != "${{ matrix.shard }}" {
		t.Errorf("macos warm step UNITS = %q, want ${{ matrix.shard }}", macWarmUnits)
	}
	for _, shard := range plan.shards("macos") {
		needed := neededCargoTargets(macosShardPackages(shard), pkgToTarget)
		if len(needed) == 0 {
			continue
		}
		if !macosWarmStepCoversShard(macWarmIf, shard) {
			t.Errorf("macos warm step (if: %q) does not run for shard %q (needs %v)", macWarmIf, shard, needed)
		}
		for _, target := range needed {
			if !strings.Contains(macWarmRun, "targets+=("+target+")") {
				t.Errorf("macos warm step does not warm target %q needed by shard %q:\n%s", target, shard, macWarmRun)
			}
		}
	}

	// Windows: derive native-1..4 packages from windowsOnlyTestPackages and cli/extension shards from the workflow structure.
	winJob, ok := jobs["windows"]
	if !ok {
		t.Fatal("ci.yml has no windows job")
	}
	winWarmIdx, winFirstTestIdx := -1, -1
	var winWarmIf, winWarmRun, winWarmShard string
	for i, step := range winJob.Steps {
		switch {
		case strings.Contains(step.Run, "automation/ci/test-warm.sh --rust-only"):
			if winWarmIdx >= 0 {
				t.Errorf("windows has multiple Rust warm steps (%d and %d)", winWarmIdx, i)
			}
			winWarmIdx, winWarmIf, winWarmRun, winWarmShard = i, step.If, step.Run, step.Env["SHARD"]
		case strings.TrimSpace(step.Run) == "automation/ci/windows-cli-units.sh" || (step.If == "startsWith(matrix.shard, 'extension-host-')" && strings.Contains(step.Run, "go test")) || (step.If == "startsWith(matrix.shard, 'extension-conformance-')" && strings.Contains(step.Run, "go test")):
			if winFirstTestIdx < 0 {
				winFirstTestIdx = i
			}
		}
	}
	if winWarmIdx < 0 || winFirstTestIdx < 0 || winWarmIdx >= winFirstTestIdx {
		t.Fatalf("windows warm step (%d) must precede first Rust test step (%d)", winWarmIdx, winFirstTestIdx)
	}
	if winWarmShard != "${{ matrix.shard }}" {
		t.Errorf("windows warm step SHARD = %q, want ${{ matrix.shard }}", winWarmShard)
	}
	winNativePkgs := windowsNativePackages(t, root)
	for _, shard := range plan.shards("windows") {
		needed := neededCargoTargets(windowsShardPackages(shard, winNativePkgs), pkgToTarget)
		if len(needed) == 0 {
			continue
		}
		if !windowsWarmStepCoversShard(winWarmIf, shard) {
			t.Errorf("windows warm step (if: %q) does not run for shard %q (needs %v)", winWarmIf, shard, needed)
		}
		wantCase := "targets=(" + strings.Join(needed, " ") + ")"
		if !strings.Contains(winWarmRun, wantCase) {
			t.Errorf("windows warm step missing %q for shard %q:\n%s", wantCase, shard, winWarmRun)
		}
	}
}

func neededCargoTargets(pkgs []string, pkgToTarget map[string]string) []string {
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		if target, ok := pkgToTarget[pkg]; ok {
			seen[target] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

func macosShardPackages(shard string) []string {
	if shard == "native" {
		return []string{"ai", "internal/codingagent/tools", "internal/nativeplatform", "tui", "internal/codingagent", "test/parity/runner"}
	}
	var pkgs []string
	for unit := range strings.FieldsSeq(shard) {
		switch {
		case strings.HasPrefix(unit, "cli-"):
			pkgs = append(pkgs, "coding/cli")
		case strings.HasPrefix(unit, "extension-host-"):
			pkgs = append(pkgs, "coding/extension/host/subprocess")
		case unit == "packages":
			pkgs = append(pkgs, "env", "coding/piglet", "chord/nextnode/plugins")
		}
	}
	return pkgs
}

func windowsNativePackages(t *testing.T, root string) []string {
	t.Helper()
	excluded := map[string]bool{
		"coding/extension/host/runtimecell": true,
		"coding/extension/host/subprocess":  true,
		"test/extension-conformance":        true,
		"coding/cli":                        true,
	}
	var pkgs []string
	for _, full := range windowsOnlyTestPackages(t, root) {
		rel := strings.TrimPrefix(full, modulePath+"/")
		if !excluded[rel] {
			pkgs = append(pkgs, rel)
		}
	}
	return pkgs
}

func windowsShardPackages(shard string, nativePkgs []string) []string {
	switch {
	case strings.HasPrefix(shard, "cli-"):
		return []string{"coding/cli"}
	case shard == "extension-host-1":
		return []string{"coding/extension/host/runtimecell", "coding/extension/host/subprocess"}
	case strings.HasPrefix(shard, "extension-host-"):
		return []string{"coding/extension/host/subprocess"}
	case strings.HasPrefix(shard, "extension-conformance-"):
		return []string{"test/extension-conformance"}
	case strings.HasPrefix(shard, "native-"):
		return nativePkgs
	default:
		return nil
	}
}

func windowsWarmStepCoversShard(expr, shard string) bool {
	if strings.Contains(expr, "matrix.shard != '"+shard+"'") {
		return false
	}
	switch {
	case strings.HasPrefix(shard, "cli-"):
		return strings.Contains(expr, "startsWith(matrix.shard, 'cli-')")
	case strings.HasPrefix(shard, "extension-"):
		return strings.Contains(expr, "startsWith(matrix.shard, 'extension-')")
	default:
		return expr == ""
	}
}

// Makefile exports CARGO_TARGET_DIR ?= $(CURDIR)/tmp/test-fixtures/rust-target for targets that fill it via
// test-fixtures.sh. test-race-nightly does not run test-fixtures.sh (nightly warms SharedCargoTargetDir via
// test-warm.sh --rust-only instead), so it must drop Makefile's unfilled default while honoring an explicit
// CARGO_TARGET_DIR from the caller's environment or command line.
func TestMakeTestRaceNightlyDropsDefaultCargoTargetDir(t *testing.T) {
	root := repoRoot(t)
	runDry := func(extraEnv []string, extraArgs ...string) string {
		t.Helper()
		args := append([]string{"-C", root, "-n", "test-race-nightly"}, extraArgs...)
		cmd := exec.Command("make", args...)
		var env []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "CARGO_TARGET_DIR=") {
				env = append(env, kv)
			}
		}
		cmd.Env = append(env, extraEnv...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("make %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := runDry(nil); !strings.HasPrefix(got, "env -u CARGO_TARGET_DIR ") {
		t.Errorf("default make -n test-race-nightly = %q, want env -u CARGO_TARGET_DIR prefix", got)
	}
	if got := runDry([]string{"CARGO_TARGET_DIR=/tmp/explicit"}); strings.Contains(got, "env -u CARGO_TARGET_DIR") {
		t.Errorf("env-override make -n test-race-nightly = %q, must keep explicit CARGO_TARGET_DIR", got)
	}
	if got := runDry(nil, "CARGO_TARGET_DIR=/tmp/cli"); strings.Contains(got, "env -u CARGO_TARGET_DIR") {
		t.Errorf("cli-override make -n test-race-nightly = %q, must keep command-line CARGO_TARGET_DIR", got)
	}
}
