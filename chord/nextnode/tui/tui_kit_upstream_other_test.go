//go:build !unix

package tui

import "testing"

// signalInspector does nothing: Windows has no SIGUSR1 (Pi guards its listener with `process.platform !== "win32"`).
func signalInspector(t *testing.T) { t.Helper() }
