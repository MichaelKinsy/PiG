package cli

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
)

// Pi main.ts:680-701 selects the Session before validating a supplied name. Selection errors and creation warnings remain observable even when the name is invalid.
func TestCLINameValidationFollowsSessionSelection(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name   string
		args   []string
		stderr string
	}{
		{"missing session", []string{"--session", "notfound", "--name", ""}, "No session found matching 'notfound'\n"},
		{"missing fork", []string{"--fork", "notfound", "--name", "   "}, "No session found matching 'notfound'\n"},
		{"new exact ID warning", []string{"--session-id", "name-order-new", "--name", ""}, "Warning: No project session found with id 'name-order-new'; creating a new session with that id.\nError: --name requires a non-empty value\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--offline", "--no-extensions", "--session-dir", t.TempDir(), "-p"}, tc.args...)
			cmd := exec.CommandContext(t.Context(), binary, args...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+t.TempDir(), "FORCE_COLOR=0")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if err == nil || cmd.ProcessState.ExitCode() != 1 || stdout.Len() != 0 || stderr.String() != tc.stderr {
				t.Fatalf("error=%v stdout=%q stderr=%q, want exit1/empty stdout/%q", err, stdout.String(), stderr.String(), tc.stderr)
			}
		})
	}
}
