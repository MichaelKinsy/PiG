// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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

func TestLinuxTestShardsPartitionPackages(t *testing.T) {
	root := t.TempDir()
	copyCIFixture(t, root, "automation/ci/test-grouped.sh")
	writeCIFixture(t, root, "automation/ci/test-fixtures.sh", "#!/bin/sh\nprintf 'export CI_TEST_FIXTURES=ready\\n'\n")
	writeCIFixture(t, root, "bin/go", `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  list)
    shift
    if [[ "$*" == ./... ]]; then
      printf '%s\n' ./alpha ./beta ./cmd/pig ./coding/extension/host/subprocess ./tests/extension-conformance
    else
      printf '%s\n' "$@"
    fi
    ;;
  test)
    test "$CI_TEST_FIXTURES" = ready
    shift 3
    printf '%s\n' "$@" >> "$TEST_LOG"
    ;;
  *) exit 99 ;;
esac
`)
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(root, "packages")
	t.Setenv("TEST_LOG", log)
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
