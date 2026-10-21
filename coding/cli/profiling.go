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

// exitProcess releases startup extension ownership and writes profiles before
// exiting. os.Exit skips deferred calls, so main uses this on every exit path.
func exitProcess(code int) {
	stopAutomaticExtensionCacheGC()
	stopModelServices()
	stopStartupExtensions()
	codingagent.RestoreStdout()
	stopProfiles()
	// pig additive (D102): every exit through here is requested or prints its reason, so the session marker goes.
	codingagent.EndSessionMarker()
	// Pi's process 'exit' handler restores the terminal modes of a TUI that exits without stop() (9df819de, #11170); os.Exit runs no handlers.
	tui.RestoreTerminalOnExit()
	os.Exit(code)
}
