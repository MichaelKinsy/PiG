// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package ciimages

import (
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
// Windows build selects: a _windows_test.go name or a "//go:build windows"
// line. A file whose constraint also needs a custom tag (integration, parity)
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
		windowsOnly := constraint == "windows" || strings.HasSuffix(name, "_windows_test.go") && (constraint == "" || constraint == "windows")
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
	output, err := cmd.Output()
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
// and the cli-N shards must together test every cmd/pig shard that make
// test-cli runs on Linux, each shard on its own job.
func TestWindowsShardsTestEverySelectedPackage(t *testing.T) {
	job, ok := verificationJobs(t)["windows"]
	if !ok {
		t.Fatal("ci.yml has no windows job")
	}
	shards, cliPkg := cliShards(t)
	cliPkg = strings.TrimPrefix(cliPkg, "./")
	// One matrix entry per cmd/pig shard, so the shards run in parallel on separate runners.
	wantMatrix := []string{"native"}
	for shard := 1; shard <= shards; shard++ {
		wantMatrix = append(wantMatrix, "cli-"+strconv.Itoa(shard))
	}
	wantMatrix = append(wantMatrix, "extension-host", "extension-conformance")
	if !slices.Equal(job.Strategy.Matrix.Shard, wantMatrix) {
		t.Fatalf("windows shards = %v, want %v", job.Strategy.Matrix.Shard, wantMatrix)
	}
	shardStep := regexp.MustCompile(`(?m)^\s*run=\$\(automation/ci/test-shard-pattern\.sh "\$\{CLI_SHARD#cli-\}" ([0-9]+) \./` + regexp.QuoteMeta(cliPkg) + `\)\n\s*go test -timeout 30m -run "\$run" \./` + regexp.QuoteMeta(cliPkg) + `\n`)
	cliShardSteps := 0
	selectLine := ""
	extensionRuns := ""
	conformanceRuns := ""
	for _, step := range job.Steps {
		if match := shardStep.FindStringSubmatch(step.Run); match != nil {
			if step.If != "startsWith(matrix.shard, 'cli-')" {
				t.Errorf("cmd/pig shard step runs when %q, want only the cli-N shards", step.If)
			}
			if match[1] != strconv.Itoa(shards) {
				t.Errorf("cmd/pig shard step runs of %s shards, want %d as make test-cli runs", match[1], shards)
			}
			cliShardSteps++
		}
		if strings.Contains(step.Run, "windows-test-packages.sh") {
			if step.If != "matrix.shard == 'native'" {
				t.Errorf("package selection step runs when %q, want the native shard", step.If)
			}
			for line := range strings.SplitSeq(step.Run, "\n") {
				if strings.Contains(line, "windows-test-packages.sh") {
					selectLine = line
				}
			}
			if !strings.Contains(step.Run, "go test -timeout 30m $packages") {
				t.Errorf("native shard does not test the selected packages:\n%s", step.Run)
			}
		}
		if step.If == "matrix.shard == 'extension-host'" {
			extensionRuns += step.Run
		}
		if step.If == "matrix.shard == 'extension-conformance'" {
			conformanceRuns += step.Run
		}
	}
	if selectLine == "" {
		t.Fatal("no windows step runs automation/ci/windows-test-packages.sh")
	}
	excluded := regexp.MustCompile(`-e '/([^']+)\$'`).FindAllStringSubmatch(selectLine, -1)
	if len(excluded) == 0 {
		t.Fatalf("expected the native shard to drop the extension host packages: %s", selectLine)
	}
	// Exactly one step serves every cli-N job, and it derives N from the matrix value, so each shard 1..shards runs once, on its own job.
	if cliShardSteps != 1 {
		t.Errorf("windows job has %d cmd/pig shard steps, want one step run by each cli-N job", cliShardSteps)
	}
	droppedCLI := false
	for _, match := range excluded {
		if match[1] == cliPkg {
			droppedCLI = true
			continue
		}
		if !regexp.MustCompile(`go test [^\n]*\./` + regexp.QuoteMeta(match[1]) + `(\s|$)`).MatchString(extensionRuns) {
			t.Errorf("native shard drops %s but the extension-host shard does not test it", match[1])
		}
	}
	if !droppedCLI {
		t.Errorf("native shard tests %s whole as well as in its shards: %s", cliPkg, selectLine)
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
