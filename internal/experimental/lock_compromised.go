package experimental

// Ports packages/coding-agent/src/experimental/server.ts
// Ports packages/coding-agent/src/experimental/session-worker.ts

import (
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// terminateOnLockCompromise is proper-lockfile 4.1.2's default onCompromised (lib/lockfile.js:213, `(err) => { throw err; }`), which runs from the heartbeat timer callback. server.ts:113 (launcher), server.ts:201 (activation) and session-worker.ts:533 (Session ownership) do not override it and no uncaughtException handler exists, so Node prints the inspected error and exits with status 1 without running Session worker cleanup or sending worker_failed. proper-lockfile's exit hook (lib/lockfile.js:331-337) still removes every other held lock directory, so pilock.RemoveHeldLocks runs first. Node adds no "Uncaught" prefix and prints the JavaScript source frame and runtime version, which have no Go counterpart.
var terminateOnLockCompromise = func(err error) {
	text := err.Error()
	if compromised := (*pilock.CompromisedError)(nil); errors.As(err, &compromised) {
		text = compromised.Inspect()
	}
	fmt.Fprintf(os.Stderr, "%s\n", text)
	pilock.RemoveHeldLocks()
	os.Exit(1)
}

// lockCompromised adapts the replaceable process-termination policy to pilock.AcquireOptions.OnCompromised.
func lockCompromised(err error) { terminateOnLockCompromise(err) }
