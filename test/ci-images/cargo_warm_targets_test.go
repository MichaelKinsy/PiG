// SPDX-License-Identifier: MIT

package ciimages

import (
	"io/fs"
	"maps"
	"os"
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
	used := map[string]string{}
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if file != root && (name == "testdata" || name == "node_modules" || name == "target" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
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
			used[string(match[1])] = filepath.ToSlash(rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(used) == 0 {
		t.Fatal("no test calls testenv.SharedCargoTargetDir; the pattern in this test is stale")
	}

	script, err := os.ReadFile(repoScript(t, "automation/ci/test-warm.sh"))
	if err != nil {
		t.Fatal(err)
	}
	warmed := map[string]bool{}
	loop := warmPackageLoop.FindSubmatch(script)
	if loop == nil {
		t.Fatal("automation/ci/test-warm.sh has no `for pkg in ...; do` loop over the packages that build Rust cells")
	}
	for _, pkg := range strings.Fields(string(loop[1])) {
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
