package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// suspendableOraclePackages is the packages directory of the Pi the oracle tests of suspendable work compare with. Pi 473515f9c
// added the work (signals, tool parks, hook tasks, the generation at task version 3) after the committed pin, 1.5.2, so the pin's
// mirror has no durable/src/signals.ts and cannot answer: the Go port carries the work ahead of the pin, and these tests run
// with the effective pin overridden to that commit, `make test-carried-oracles` (PI_UPSTREAM_REF; the oracle is then the source
// build of that commit, as in any run that overrides the pin). A pin that reaches the commit runs them in every test run, and
// the skip, the make target and CARRIED_ORACLE_* go with that pin bump. The skip is the one pioracle.Bin gives an oracle that is
// not built: the oracle cannot answer, and the make target is where it does.
func suspendableOraclePackages(t testing.TB) string {
	t.Helper()
	packages := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages")
	if _, err := os.Stat(filepath.Join(packages, "durable", "src", "signals.ts")); err != nil {
		t.Skipf("the pin's Pi mirror has no suspendable work (Pi 473515f9c, after the committed pin): run make test-carried-oracles")
	}
	return packages
}
