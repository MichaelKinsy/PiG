// SPDX-License-Identifier: MIT

package pioracle

import (
	"os"
	"testing"
)

// RequireEnv is the variable a gate sets when it installed the Pi oracle (make model-data-check, the Linux jobs of the CI workflow, the stamping precheck). With it set, a test whose Pi source or published data is absent fails; without it the test skips, as it does on a developer's machine that never built the oracle. A gate that cannot install the oracle does not set it, and its skips are printed by go test -v like any other.
const RequireEnv = "PIG_REQUIRE_PI_ORACLE"

// Missing ends a test whose Pi source or published data (what) is not installed (err says why; nil when the lookup found nothing and has no error). Where RequireEnv is set it fails, because an unavailable dependency is a blocker, not a passing run: a test that skips there lets a change that breaks what it compares pass every gate. Elsewhere it skips with the reason.
func Missing(t testing.TB, what string, err error) {
	t.Helper()
	reason := "not found"
	if err != nil {
		reason = err.Error()
	}
	if v := os.Getenv(RequireEnv); v != "" && v != "0" {
		t.Fatalf("pioracle: %s is not installed (%s) and %s=%s says this gate installs the Pi oracle: an unavailable oracle is a blocker, not passing evidence; run `make upstream-mirror`", what, reason, RequireEnv, v)
	}
	t.Skipf("%s is not installed: %s", what, reason)
}
