// SPDX-License-Identifier: MIT

package cli

import (
	"os"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// stopProfiles writes the PIG_PROFILE profiles that main started. It is a no-op
// when PIG_PROFILE is unset.
var stopProfiles = func() {}

// stopModelServices cancels and drains Services-owned model work while extension transports are still available.
var stopModelServices = func() {}

// stopGCPolicy ends the live-heap watcher of the GC policy (D175 for print, json and rpc runs, D621 for interactive runs) and waits for it to return. It runs before stopProfiles,
// which counts the goroutines left at exit. It is a no-op until Main applies the policy.
var stopGCPolicy = func() {}

// exitProcess releases startup extension ownership and writes profiles before
// exiting. os.Exit skips deferred calls, so main uses this on every exit path.
func exitProcess(code int) {
	stopAutomaticExtensionCacheGC()
	stopModelServices()
	stopStartupExtensions()
	codingagent.RestoreStdout()
	stopGCPolicy()
	stopProfiles()
	// pig additive (D102): every exit through here is requested or prints its reason, so the session marker goes.
	codingagent.EndSessionMarker()
	// Pi's process 'exit' handler restores the terminal modes of a TUI that exits without stop() (9df819de, #11170); os.Exit runs no handlers.
	tui.RestoreTerminalOnExit()
	// pig additive (D102): an emergency exit in flight ends the process with its 129, and a claimed exit.log line is flushed first.
	codingagent.BeforeExit()
	os.Exit(code)
}
