package interop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// skipWithoutSuspendableWork skips a test that compares the Go store with Pi's rows or views of work that Pi 473515f9c added after the
// committed pin: the task and generation versions of suspendable work (signals, tool parks, hook tasks) differ from the pin's Pi, whose
// mirror has no durable/src/signals.ts. `make test-carried-oracles` runs these tests with the pin overridden to that commit, whose
// build is then the Pi Durable under durable/interop/node_modules. The skip, the make variable and the target go with the pin bump.
func skipWithoutSuspendableWork(t *testing.T) {
	t.Helper()
	signals := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "durable", "src", "signals.ts")
	if _, err := os.Stat(signals); err != nil {
		t.Skip("the pin's Pi mirror has no suspendable work (Pi 473515f9c, after the committed pin): run make test-carried-oracles")
	}
}
