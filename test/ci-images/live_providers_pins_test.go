// SPDX-License-Identifier: MIT

package ciimages

import (
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
