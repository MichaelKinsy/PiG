// SPDX-License-Identifier: MIT

package ciimages

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const modulePath = "github.com/MichaelKinsy/PiG"

// windowsOnlyTestPackages walks the root module for test files that only a
// Windows build selects: a _windows_test.go name or a "//go:build windows" or
// "//go:build !unix" line. A file whose constraint also needs a custom tag (integration, parity)
// is not selected by a default go test and is left out.
func windowsOnlyTestPackages(t *testing.T, root string) []string {
	t.Helper()
	packages := map[string]bool{}
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if file == root {
				return nil
			}
			if name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(file, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		constraint := buildConstraint(t, file)
		// "!unix" selects the file on Windows and on no Linux or macOS build.
		windowsOnly := constraint == "windows" || constraint == "!unix" || strings.HasSuffix(name, "_windows_test.go") && (constraint == "" || constraint == "windows")
		if windowsOnly {
			rel, err := filepath.Rel(root, filepath.Dir(file))
			if err != nil {
				return err
			}
			packages[path.Join(modulePath, filepath.ToSlash(rel))] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(packages))
}

func buildConstraint(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if constraint, ok := strings.CutPrefix(line, "//go:build "); ok {
			return strings.TrimSpace(constraint)
		}
		if strings.HasPrefix(line, "package ") {
			break
		}
	}
	return ""
}

func runWindowsTestPackages(t *testing.T, env ...string) (string, error) {
	t.Helper()
	cmd := testenv.ScriptCommand(t, filepath.Join(repoRoot(t), "automation", "ci", "windows-test-packages.sh"))
	cmd.Env = append(os.Environ(), env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		err = fmt.Errorf("%w\nstderr:\n%s", err, stderr.String())
	}
	return string(output), err
}

// The native Windows job runs the packages the script prints. It must print
// every package that holds a Windows-only test file, and nothing else.
func TestWindowsTestPackagesSelectEveryWindowsOnlyTestPackage(t *testing.T) {
	want := windowsOnlyTestPackages(t, repoRoot(t))
	if len(want) == 0 {
		t.Fatal("found no Windows-only test file")
	}
	output, err := runWindowsTestPackages(t)
	if err != nil {
		t.Fatalf("windows-test-packages.sh: %v\n%s", err, output)
	}
	got := strings.Fields(output)
	if !slices.Equal(got, want) {
		t.Fatalf("windows-test-packages.sh selected\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A go list failure must fail the script. An empty list with a zero exit
// would let the native Windows job run no Windows test.
func TestWindowsTestPackagesFailWhenGoListFails(t *testing.T) {
	output, err := runWindowsTestPackages(t, "GOFLAGS=-mod=bogus")
	if err == nil {
		t.Fatalf("windows-test-packages.sh succeeded with a failing go list; output:\n%s", output)
	}
	if strings.TrimSpace(output) != "" {
		t.Fatalf("windows-test-packages.sh printed packages after go list failed:\n%s", output)
	}
}

// The native shard drops the extension host packages and cmd/pig from the
// script's list. The extension-host shard must test each dropped extension host
// package, the extension-conformance shard must test the conformance package,
// and the cli jobs must together test every cmd/pig shard that make
// test-cli runs on Linux, each shard once, in its own test process.
func TestWindowsShardsTestEverySelectedPackage(t *testing.T) {
	job, ok := verificationJobs(t)["windows"]
	if !ok {
		t.Fatal("ci.yml has no windows job")
	}
	shards, cliPkg := cliShards(t)
	cliPkg = strings.TrimPrefix(cliPkg, "./")
	// Every unit runs in exactly one job: the native and extension units each on their own job, the cmd/pig shards paired so a job builds the test binary once.
	wantUnits := []string{"native-1", "native-2", "native-3", "native-4"}
	for shard := 1; shard <= shards; shard++ {
		wantUnits = append(wantUnits, "cli-"+strconv.Itoa(shard))
	}
	wantUnits = append(wantUnits, "extension-host-1", "extension-host-2", "extension-host-3", "extension-conformance-1", "extension-conformance-2")
	var gotUnits []string
	for _, entry := range planFor(t, "merge_group", "").shards("windows") {
		units := strings.Fields(entry)
		for _, unit := range units {
			if len(units) > 1 && !strings.HasPrefix(unit, "cli-") {
				t.Errorf("windows job %q groups %s; only cmd/pig shards share a job", entry, unit)
			}
		}
		gotUnits = append(gotUnits, units...)
	}
	slices.Sort(gotUnits)
	slices.Sort(wantUnits)
	if !slices.Equal(gotUnits, wantUnits) {
		t.Fatalf("windows units = %v, want each of %v exactly once", gotUnits, wantUnits)
	}
	// The runner lists and runs the prebuilt test binary from the package directory (go test hangs on GitHub's Windows runners before the binary starts, golang/go#81368), streams every test with -test.v, and puts the 25-minute alarm and its stack dump inside the 60-minute job. The compile and each listing (it runs TestMain) are bounded on their own.
	runner, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "windows-cli-units.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cliRunner := regexp.MustCompile(`package=` + regexp.QuoteMeta(cliPkg) + `\n(?:.*\n)*?.*timeout 10m go test -c -o "\$bin" \./\$package; then\n(?:.*\n)*?.*run=\$\(TEST_SHARD_BINARY="\$bin" timeout 5m automation/ci/test-shard-pattern\.sh "\$shard" ([0-9]+) \./\$package\); then\n(?:.*\n)*?\s*\(cd "\$package" && "\$bin" -test\.paniconexit0 -test\.v -test\.timeout 25m -test\.run "\$run"\) \|\| failed=1\n`)
	if match := cliRunner.FindStringSubmatch(string(runner)); match == nil {
		t.Errorf("windows-cli-units.sh does not build the %s test binary once and run each shard of it from the package directory", cliPkg)
	} else if match[1] != strconv.Itoa(shards) {
		t.Errorf("windows-cli-units.sh runs of %s shards, want %d as make test-cli runs", match[1], shards)
	}
	cliShardSteps := 0
	selectLine := ""
	extensionRuns := ""
	conformanceRuns := ""
	for _, step := range job.Steps {
		if strings.TrimSpace(step.Run) == "automation/ci/windows-cli-units.sh" {
			if step.If != "startsWith(matrix.shard, 'cli-')" || step.Env["UNITS"] != "${{ matrix.shard }}" {
				t.Errorf("cmd/pig shard step runs when %q with UNITS %q, want the cli jobs' own matrix value", step.If, step.Env["UNITS"])
			}
			cliShardSteps++
		}
		if strings.Contains(step.Run, "windows-test-packages.sh") {
			t.Errorf("a workflow step runs windows-test-packages.sh directly; the native-N jobs must run windows-native-shard.sh")
		}
		if strings.Contains(step.Run, "windows-native-shard.sh") {
			if step.If != "startsWith(matrix.shard, 'native-')" {
				t.Errorf("package selection step runs when %q, want the native-N shards", step.If)
			}
			for line := range strings.SplitSeq(step.Run, "\n") {
				if strings.Contains(line, "windows-native-shard.sh") {
					selectLine = line
				}
			}
			if !strings.Contains(selectLine, `windows-native-shard.sh "${NATIVE_SHARD#native-}"`) || step.Env["NATIVE_SHARD"] != "${{ matrix.shard }}" {
				t.Errorf("native step does not derive its shard from the matrix value: %s", selectLine)
			}
			if !strings.Contains(step.Run, "go test -timeout 30m $packages") {
				t.Errorf("native shard does not test the selected packages:\n%s", step.Run)
			}
		}
		if step.If == "startsWith(matrix.shard, 'extension-host-')" {
			extensionRuns += step.Run
		}
		if step.If == "startsWith(matrix.shard, 'extension-conformance-')" {
			conformanceRuns += step.Run
		}
	}
	if selectLine == "" {
		t.Fatal("no windows step runs automation/ci/windows-native-shard.sh")
	}
	shardScript, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "windows-native-shard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	exclusions, _, _ := strings.Cut(string(shardScript), "shard1=")
	excluded := regexp.MustCompile(`-e "\$module/([^"]+)"`).FindAllStringSubmatch(exclusions, -1)
	if len(excluded) == 0 {
		t.Fatalf("expected the native shards to drop the extension host packages: %s", shardScript)
	}
	// Exactly one step serves every cli job, and it runs the shards its matrix value names, so each shard 1..shards runs once.
	if cliShardSteps != 1 {
		t.Errorf("windows job has %d cmd/pig shard steps, want one step run by each cli-N job", cliShardSteps)
	}
	droppedCLI := false
	for _, match := range excluded {
		if match[1] == cliPkg {
			droppedCLI = true
			continue
		}
		if !regexp.MustCompile(`go test [^\n]*\./` + regexp.QuoteMeta(match[1]) + `(\s|$)`).MatchString(extensionRuns + conformanceRuns) {
			t.Errorf("native shard drops %s but neither the extension-host nor the extension-conformance shard tests it", match[1])
		}
	}
	if !droppedCLI {
		t.Errorf("native shards test %s whole as well as in its shards", cliPkg)
	}
	// The two jobs of each extension pair split the package's tests with test-shard-pattern.sh, so each test runs once.
	for _, want := range []string{`extension-host-*) package=./coding/extension/host/subprocess shards=3`, `extension-conformance-*) package=./test/extension-conformance shards=2`, `test-shard-pattern.sh "${EXTENSION_SHARD##*-}" "$shards" "$package"`} {
		found := false
		for _, step := range job.Steps {
			if strings.Contains(step.Run, want) && step.If == "startsWith(matrix.shard, 'extension-')" {
				found = true
			}
		}
		if !found {
			t.Errorf("no extension shard-selection step contains %q", want)
		}
	}
	const conformancePkg = "test/extension-conformance"
	conformanceRun := regexp.MustCompile(`go test [^\n]*\./` + regexp.QuoteMeta(conformancePkg) + `(\s|$)`)
	if !conformanceRun.MatchString(conformanceRuns) {
		t.Errorf("extension-conformance shard does not test %s", conformancePkg)
	}
	if conformanceRun.MatchString(extensionRuns) {
		t.Errorf("extension-host shard also tests %s, so the shards do not split the extension work", conformancePkg)
	}
}

// The three native jobs must together test exactly the packages that windows-test-packages.sh prints, less the packages the extension and cli jobs test, each package once.
func TestWindowsNativeShardsPartitionTheSelectedPackages(t *testing.T) {
	root := repoRoot(t)
	output, err := runWindowsTestPackages(t)
	if err != nil {
		t.Fatalf("windows-test-packages.sh: %v\n%s", err, output)
	}
	want := map[string]bool{}
	for pkg := range strings.FieldsSeq(output) {
		switch strings.TrimPrefix(pkg, modulePath+"/") {
		case "coding/extension/host/runtimecell", "coding/extension/host/subprocess", "test/extension-conformance", "coding/cli":
		default:
			want[pkg] = true
		}
	}
	seen := map[string]int{}
	for shard := 1; shard <= 4; shard++ {
		cmd := testenv.ScriptCommand(t, filepath.Join(root, "automation", "ci", "windows-native-shard.sh"), strconv.Itoa(shard))
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("windows-native-shard.sh %d: %v\nstderr:\n%s", shard, err, stderr.String())
		}
		for pkg := range strings.FieldsSeq(string(out)) {
			seen[pkg]++
		}
	}
	for pkg := range want {
		if seen[pkg] != 1 {
			t.Errorf("%s runs in %d native shards, want 1", pkg, seen[pkg])
		}
	}
	for pkg := range seen {
		if !want[pkg] {
			t.Errorf("native shards run %s, which is not a selected Windows test package", pkg)
		}
	}
}

// windows-native-shard.sh checks each printed package against the selected list. Under pipefail, feeding that list to
// `grep -q` through a pipe fails when grep exits on its match before the shell has written the rest: the writer gets
// SIGPIPE, the pipeline fails, and the script reports a selected package as unselected. Bash's printf writes one line per
// write, so a busy runner can lose that race on a short list; a list longer than the pipe buffer loses it every time.
// The shard script runs here against a stand-in package list of that length.
func TestWindowsNativeShardChecksALongPackageListWithoutSIGPIPE(t *testing.T) {
	root := t.TempDir()
	ci := filepath.Join(root, "automation", "ci")
	if err := os.MkdirAll(ci, 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "windows-native-shard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ci, "windows-native-shard.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	var list strings.Builder
	for _, pkg := range []string{"ai", "coding", "internal/codingagent", "tui"} {
		list.WriteString(modulePath + "/" + pkg + "\n")
	}
	var padding []string
	long := strings.Repeat("segment/", 30)
	for i := range 300 {
		pkg := fmt.Sprintf("%s/generated/%spackage%03d", modulePath, long, i)
		padding = append(padding, pkg)
		list.WriteString(pkg + "\n")
	}
	if list.Len() <= 1<<16 {
		t.Fatalf("the stand-in list is %d bytes; it must exceed a 64 KiB pipe buffer", list.Len())
	}
	stub := "#!/usr/bin/env bash\ncat <<'LIST'\n" + list.String() + "LIST\n"
	if err := os.WriteFile(filepath.Join(ci, "windows-test-packages.sh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := testenv.ScriptCommand(t, filepath.Join(ci, "windows-native-shard.sh"), "4")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("windows-native-shard.sh 4: %v\nstderr:\n%s", err, stderr.String())
	}
	if got := strings.Fields(string(out)); !slices.Equal(got, padding) {
		t.Fatalf("shard 4 printed %d packages, want the %d unnamed ones", len(got), len(padding))
	}
}
