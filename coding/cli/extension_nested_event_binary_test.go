package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const nestedEventExtension = `export default function (pi) {
  pi.on("session_info_changed", (e) => { process.stderr.write("event " + e.name + "\n"); });
  pi.on("thinking_level_select", (e) => { process.stderr.write("event " + e.level + "\n"); });
  pi.registerCommand("probe", {
    handler: async (args) => {
      if (args === "name") pi.setSessionName("x");
      else pi.setThinkingLevel("high");
      process.stderr.write("tools " + pi.getActiveTools().length + "\n");
    },
  });
}
`

// Pi 1.1.0 emits session_info_changed and thinking_level_select with void runner.emit inside setSessionName and setThinkingLevel (agent-session.ts:2629, 3946), so a subscribed handler's synchronous prefix runs before the setter returns and a later synchronous API call proceeds: Pi prints "event ..." then "tools 4". The Node runtime held the event while a synchronous host call waited, and the host's call lane waited for the event's handler to start, so PiG hung.
func TestExtensionSetterEventRunsBeforeALaterSynchronousCall(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	extPath := filepath.Join(t.TempDir(), "probe.mjs")
	if err := os.WriteFile(extPath, []byte(nestedEventExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, model, arg, want string }{
		{name: "session name", model: "test-faux/faux-1", arg: "name", want: "event x\ntools 4\n"},
		{name: "thinking level", model: "openai/gpt-5", arg: "think", want: "event high\ntools 4\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			ctx := testbudget.Context(t)
			cmd := exec.CommandContext(ctx, bin, "-p", "--no-session", "--no-extensions", "--offline", "-e", extPath, "--model", tc.model, "/probe "+tc.arg)
			cmd.Dir = agentDir
			cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1", "OPENAI_API_KEY=test")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatalf("pig did not finish: the setter's event and the later synchronous call deadlocked\nstderr: %s", stderr.String())
			}
			if err != nil {
				t.Fatalf("pig: %v\nstderr: %s", err, stderr.String())
			}
			if got := stderr.String(); got != tc.want {
				t.Fatalf("stderr = %q, want %q", got, tc.want)
			}
		})
	}
}
