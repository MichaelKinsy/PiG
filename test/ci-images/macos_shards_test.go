// SPDX-License-Identifier: MIT

package ciimages

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// macOS runs the packages whose tests reached macOS-only failures (104-byte
// socket paths, /var -> /private/var temporary directories, small PTY buffers,
// fsnotify gaps) next to the native smoke run: every cmd/pig shard that make
// test-cli runs on Linux, each on its own job, and each of the other packages.
func TestMacOSShardsTestTheMacOSSensitivePackages(t *testing.T) {
	job, ok := verificationJobs(t)["macos"]
	if !ok {
		t.Fatal("ci.yml has no macos job")
	}
	shards, cliPkg := cliShards(t)
	wantMatrix := []string{"native"}
	for shard := 1; shard <= shards; shard++ {
		wantMatrix = append(wantMatrix, "cli-"+strconv.Itoa(shard))
	}
	wantMatrix = append(wantMatrix, "extension-host-1", "extension-host-2", "experimental", "packages")
	if !slices.Equal(job.Strategy.Matrix.Shard, wantMatrix) {
		t.Fatalf("macos shards = %v, want %v", job.Strategy.Matrix.Shard, wantMatrix)
	}
	if job.Strategy.FailFast == nil || *job.Strategy.FailFast {
		t.Error("macos shards must all run when one fails")
	}
	if job.Timeout <= 0 || job.Timeout > 60 {
		t.Errorf("macos timeout = %d minutes, want 1 to 60", job.Timeout)
	}
	cliStep := regexp.MustCompile(`(?m)^\s*run=\$\(automation/ci/test-shard-pattern\.sh "\$\{CLI_SHARD#cli-\}" ([0-9]+) ` + regexp.QuoteMeta(cliPkg) + `\)\n\s*go test -timeout 30m -run "\$run" ` + regexp.QuoteMeta(cliPkg) + `\n`)
	hostStep := regexp.MustCompile(`test-shard-pattern\.sh "\$\{EXTENSION_SHARD##\*-\}" 2 \./coding/extension/host/subprocess\)\n\s*go test -timeout 30m -run "\$run" \./coding/extension/host/subprocess\n`)
	steps := map[string]bool{}
	for _, step := range job.Steps {
		switch {
		case strings.HasPrefix(step.Uses, "actions/setup-python@") && step.With["python-version"] == "3.12":
			steps["python"] = true
		case step.If == "matrix.shard != 'native'" && strings.Contains(step.Run, `rustup default "$RUST_VERSION"`):
			steps["rust"] = true
		case strings.Contains(step.Run, "npm ci --prefix test/parity/interface-extractor "):
			steps["typescript"] = true
		case step.If == "matrix.shard == 'native'" && strings.Contains(step.Run, "go test ./ai ./internal/codingagent/tools ./internal/nativeplatform ./tui"):
			steps["native"] = true
		case step.If == "startsWith(matrix.shard, 'cli-')" && cliStep.MatchString(step.Run):
			if match := cliStep.FindStringSubmatch(step.Run); match[1] != strconv.Itoa(shards) {
				t.Errorf("macos cmd/pig step runs of %s shards, want %d as make test-cli runs", match[1], shards)
			}
			steps["cli"] = true
		case step.If == "startsWith(matrix.shard, 'extension-host-')" && hostStep.MatchString(step.Run):
			steps["host"] = true
		case step.If == "matrix.shard == 'experimental'" && strings.Contains(step.Run, "go test -timeout 30m ./internal/experimental\n"):
			steps["experimental"] = true
		case step.If == "matrix.shard == 'packages'" && strings.Contains(step.Run, "go test -timeout 30m ./env ./coding/piglet\n"):
			steps["packages"] = true
		}
	}
	// The shards run tests that build Rust extensions, drive Python harnesses and transpile Pi's source with the locked TypeScript (internal/experimental's Radius probe), so they need the toolchains Linux and Windows pin.
	for _, name := range []string{"native", "cli", "host", "experimental", "packages", "python", "rust", "typescript"} {
		if !steps[name] {
			t.Errorf("no macos step provides %s as declared", name)
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
