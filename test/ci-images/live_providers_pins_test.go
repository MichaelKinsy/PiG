// SPDX-License-Identifier: MIT

package ciimages

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The live-providers workflow installs the same Rust and npm as ci.yml. It reads both pins from the env block of ci.yml, the one
// place that names them, so a pin move reaches the live run. The checks that npm is the pinned release come before the oracle is
// prepared, as in the hosted ci.yml jobs (TestHostedJobsInstallPinnedNpmBeforeOracle).
func TestLiveProvidersReadsRustAndNpmFromCI(t *testing.T) {
	job, ok := readWorkflow(t, "live-providers.yml").Jobs["live"]
	if !ok {
		t.Fatal("live-providers.yml has no live job")
	}
	derived := map[string]bool{}
	var rustInstalled, rustDefault, npmInstalled, npmVerified, oracle bool
	for _, step := range job.Steps {
		for line := range strings.SplitSeq(step.Run, "\n") {
			line = strings.TrimSpace(line)
			for _, name := range []string{"RUST_VERSION", "NPM_VERSION"} {
				if strings.HasPrefix(line, name+"=") && strings.Contains(line, ".github/workflows/ci.yml") {
					derived[name] = true
				}
			}
			switch {
			case line == `rustup toolchain install "$RUST_VERSION" --profile minimal` && derived["RUST_VERSION"]:
				rustInstalled = true
			case line == `rustup default "$RUST_VERSION"` && derived["RUST_VERSION"]:
				rustDefault = true
			case strings.Contains(line, "automation/ci/install-npm-toolchain.sh"):
				npmInstalled = true
			case line == `test "$(npm --version)" = "$NPM_VERSION"` && npmInstalled && derived["NPM_VERSION"]:
				npmVerified = true
			case strings.Contains(line, "make upstream-mirror"):
				oracle = true
				if !npmVerified {
					t.Error("live-providers.yml prepares the oracle before it installs and checks the pinned npm")
				}
			}
		}
	}
	for name, ok := range map[string]bool{
		"RUST_VERSION read from ci.yml":            derived["RUST_VERSION"],
		"NPM_VERSION read from ci.yml":             derived["NPM_VERSION"],
		`rustup toolchain install "$RUST_VERSION"`: rustInstalled,
		`rustup default "$RUST_VERSION"`:           rustDefault,
		"install-npm-toolchain.sh":                 npmInstalled,
		`npm --version = "$NPM_VERSION"`:           npmVerified,
		"make upstream-mirror":                     oracle,
	} {
		if !ok {
			t.Errorf("live-providers.yml live job lacks %s", name)
		}
	}
}

var liveBuildTag = regexp.MustCompile(`(?m)^//go:build .*\blive\b`)

// The nightly run tests the packages named by its default input, and a package that is not named is never run. The Bedrock provider moved to
// ai/providers/bedrock with its live tests, and the default still named only ./ai, so those tests dropped out of the run. Every package under ai and
// coding that holds a live-tagged test file must be one of the default packages.
func TestLiveProvidersDefaultPackagesCoverEveryLiveTaggedPackage(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/live-providers.yml"))
	if err != nil {
		t.Fatal(err)
	}
	inputDefault := regexp.MustCompile(`(?m)^      packages:\n(?:        .*\n)*?        default: '([^']*)'`).FindStringSubmatch(string(data))
	if inputDefault == nil {
		t.Fatal("live-providers.yml has no packages input default")
	}
	stepDefault := regexp.MustCompile(`PACKAGES: \$\{\{ inputs\.packages \|\| '([^']*)' \}\}`).FindStringSubmatch(string(data))
	if stepDefault == nil || stepDefault[1] != inputDefault[1] {
		t.Fatalf("the input default %q and the step's fallback %v must name the same packages", inputDefault[1], stepDefault)
	}
	covered := map[string]bool{}
	for _, pkg := range strings.Fields(inputDefault[1]) {
		covered[path.Clean(pkg)] = true
	}
	live := map[string]bool{}
	for _, top := range []string{"ai", "coding"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, "_test.go") {
				return err
			}
			head, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if len(head) > 400 {
				head = head[:400]
			}
			if liveBuildTag.Match(head) {
				rel, _ := filepath.Rel(root, filepath.Dir(p))
				live[filepath.ToSlash(rel)] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(live) == 0 {
		t.Fatal("found no live-tagged test file under ai or coding")
	}
	for pkg := range live {
		if !covered[pkg] {
			t.Errorf("%s holds live-tagged tests but the live-providers default packages %q do not name it", pkg, inputDefault[1])
		}
	}
}
