package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// suspendableOraclePackages is the packages directory of the Pi the oracle tests of suspendable work compare with. Pi 473515f9c
// added the work (signals, tool parks, hook tasks, the generation at task version 3) after the committed pin, 1.5.2, so the pin's
// mirror has no durable/src/signals.ts and cannot answer: the Go port carries the work ahead of the pin, and these tests run
// against the tree of the later commit that contains it, `make test-carried-oracles` (pioracle.OverrideTreeEnv: the packages of
// that commit's source build and an install of its packs, read in the build store). A pin that reaches the commit runs them in
// every test run, and the skip, the make target and CARRIED_ORACLE_* go with that pin bump. At the pin the oracle cannot answer,
// so the test skips; in the carried run a tree without the work fails it (pioracle.SkipAtPin).
func suspendableOraclePackages(t testing.TB) string {
	t.Helper()
	packages := pioracle.Packages(t)
	if _, err := os.Stat(filepath.Join(packages, "durable", "src", "signals.ts")); err != nil {
		pioracle.SkipAtPin(t, "the pin's Pi mirror has no suspendable work (Pi 473515f9c, after the committed pin): run make test-carried-oracles")
	}
	return packages
}
