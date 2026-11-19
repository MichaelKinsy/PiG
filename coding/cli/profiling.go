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

// terminateSessionExtensions ends the extension processes of every live Session host the interactive Session factory tracks, the replacement Sessions' included (D70), which the startup host's shutdown does not reach. It is a no-op until Main wires it.
// pig additive (D19): Pi's extensions run inside its process, so every process.exit ends them; here os.Exit would leave them running for as long as they take to end. It kills without session_shutdown, which Pi's fatal exit does not emit either, and costs nothing for a host whose processes already ended.
var terminateSessionExtensions = func() {}

// stopGCPolicy ends the live-heap watcher of the GC policy (D175 for print, json and rpc runs, D621 for interactive runs) and waits for it to return. It runs before stopProfiles,
// which counts the goroutines left at exit. It is a no-op until Main applies the policy.
var stopGCPolicy = func() {}

// exitProcess releases startup extension ownership and writes profiles before
// exiting. os.Exit skips deferred calls, so main uses this on every exit path.
func exitProcess(code int) {
	releaseForExit()
	os.Exit(code)
}

// exitProcessBySignal does the cleanup of exitProcess, then ends the process by sig as its default action does (dieBySignal), so a parent sees death by that signal.
func exitProcessBySignal(sig os.Signal) {
	releaseForExit()
	dieBySignal(sig)
}

// releaseForExit is the cleanup every exit does before the process ends.
func releaseForExit() {
	stopAutomaticExtensionCacheGC()
	stopModelServices()
	stopStartupExtensions()
	terminateSessionExtensions()
	// A settings write that a setter on another goroutine is performing finishes before the process ends.
	codingagent.FlushSettingsWrites()
	codingagent.RestoreStdout()
	stopGCPolicy()
	stopProfiles()
	// pig additive (D102): every exit through here is requested or prints its reason, so the session marker goes.
	codingagent.EndSessionMarker()
	// Pi's process 'exit' handler restores the terminal modes of a TUI that exits without stop() (9df819de, #11170); os.Exit runs no handlers.
	tui.RestoreTerminalOnExit()
	// pig additive (D102): an emergency exit in flight ends the process with its 129, and a claimed exit.log line is flushed first.
	codingagent.BeforeExit()
}
