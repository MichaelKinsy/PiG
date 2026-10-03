package main

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
)

// Implementation-derived (Pi 1.0.0 adds no test for #10236): main.ts reports "--provider requires --model" as an error
// diagnostic when --provider has no --model, instead of running another provider's default model
// (.upstream/v1.0.0/packages/coding-agent/src/main.ts:469-474). Probed against Pi 1.0.0 with
// `pi --offline --no-extensions --no-session <args>`: exit 1, empty stdout, and the stderr below.
func TestCLIProviderRequiresModelUpstream(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name   string
		args   []string
		stderr string
	}{
		{"built-in provider without a message", []string{"--provider", "anthropic", "-p"}, "Error: --provider requires --model (for example: --provider anthropic --model <pattern>)\n"},
		{"built-in provider with a message", []string{"--provider", "openai", "-p", "hi"}, "Error: --provider requires --model (for example: --provider openai --model <pattern>)\n"},
		{"unknown provider", []string{"--provider", "my-proxy", "-p", "hi"}, "Error: --provider requires --model (for example: --provider my-proxy --model <pattern>)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"--offline", "--no-extensions", "--no-session"}, tc.args...)
			cmd := exec.CommandContext(t.Context(), binary, args...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+t.TempDir(), "PI_CODING_AGENT_DIR=")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if err == nil || cmd.ProcessState.ExitCode() != 1 || stdout.String() != "" || stderr.String() != tc.stderr {
				t.Fatalf("err=%v stdout=%q stderr=%q, want exit 1 and stderr %q", err, stdout.String(), stderr.String(), tc.stderr)
			}
		})
	}
}
