package interop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// skipWithoutSuspendableWork skips a test that compares the Go store with Pi's rows or views of work that Pi 473515f9c added after the
// committed pin: the task and generation versions of suspendable work (signals, tool parks, hook tasks) differ from the pin's Pi, whose
// mirror has no durable/src/signals.ts. `make test-carried-oracles` runs these tests against the tree of the later commit that
// contains the work (pioracle.OverrideTreeEnv), whose install is then the Pi Durable the programs import (programDir), and there
// a tree without the work fails them. The skip, the make variable and the target go with the pin bump.
func skipWithoutSuspendableWork(t *testing.T) {
	t.Helper()
	signals := filepath.Join(pioracle.Packages(t), "durable", "src", "signals.ts")
	if _, err := os.Stat(signals); err != nil {
		pioracle.SkipAtPin(t, "the pin's Pi mirror has no suspendable work (Pi 473515f9c, after the committed pin): run make test-carried-oracles")
	}
}
