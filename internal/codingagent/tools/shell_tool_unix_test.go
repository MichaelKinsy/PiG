//go:build unix

package tools

import "testing"

// A signal-killed shell reports 128 + signal (upstream createLocalShellOperations).
func TestShellToolSignalExitCode(t *testing.T) {
	res := runShell(t, t.Context(), t.TempDir(), posixShellConfig(t, "bash", "bash"), bashParams{Command: "kill -9 $$"})
	if !res.IsError || res.Text() != "(no output)\n\nCommand exited with code 137" || !jsonEqual(t, res.Details, map[string]any{}) {
		t.Fatalf("result = %+v", res)
	}
}
