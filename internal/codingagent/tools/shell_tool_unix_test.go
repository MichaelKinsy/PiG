//go:build unix

package tools

import "testing"

// A signal-killed shell reports 128 + signal (upstream createLocalShellOperations). Since
// upstream 0.99.1 the non-zero exit is a returned error result with no details
// (.upstream/v0.99.1/packages/coding-agent/src/core/tools/bash.ts:401-407).
func TestShellToolSignalExitCode(t *testing.T) {
	res := runShell(t, t.Context(), t.TempDir(), posixShellConfig(t, "bash", "bash"), bashParams{Command: "kill -9 $$"})
	if !res.IsError || res.Text() != "(no output)\n\nCommand exited with code 137" || res.Details != nil {
		t.Fatalf("result = %+v", res)
	}
}
