//go:build linux

package cli

import "testing"

// seededAgentDir is a temporary agent directory whose settings.json exists, so an interactive start skips PiG's
// first-time setup (D88) in tests that are not about it.
func seededAgentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	seedFirstRunDone(t, dir)
	return dir
}
