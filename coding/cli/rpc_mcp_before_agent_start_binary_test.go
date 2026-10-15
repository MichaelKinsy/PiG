package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// builtin:mcp's before_agent_start handler awaits once (extensions/mcp/index.ts:1175-1181), and Pi 1.2.0's _startRun resumes one microtask after the async _prepareRun returns (agent-session.ts:1832-1837), so with the extension loaded Pi answers a prompt after a steer from the same input batch, whichever was read first. Without the extension (--no-extensions) the prompt answers before the steer. Probed on Pi 1.2.0 (`pi --mode rpc --no-session --offline -e test-faux-provider.ts`, with and without --no-extensions, both input orders): s,p; s,p; p,s; p,s.
func TestRPCPromptAnswersAfterTheBuiltinMCPBeforeAgentStartHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	steerFirst := `{"type":"steer","id":"s","message":"hi"}` + "\n" + `{"type":"prompt","id":"p","message":"hello"}` + "\n"
	promptFirst := `{"type":"prompt","id":"p","message":"hello"}` + "\n" + `{"type":"steer","id":"s","message":"hi"}` + "\n"
	for _, tc := range []struct {
		name  string
		input string
		extra []string
		want  []string
	}{
		{name: "builtin mcp", input: steerFirst, want: []string{"s", "p"}},
		{name: "no extensions", input: steerFirst, extra: []string{"--no-extensions"}, want: []string{"p", "s"}},
		// Pi 1.1.0 prepared inline and answered this prompt first; Pi 1.2.0's _prepareRun hop puts it after the steer.
		{name: "builtin mcp prompt first", input: promptFirst, want: []string{"s", "p"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.input
			stdinPath := filepath.Join(t.TempDir(), "commands.jsonl")
			if err := os.WriteFile(stdinPath, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			stdin, err := os.Open(stdinPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stdin.Close() }()
			// modePigCommand always passes --no-extensions, which drops builtin:mcp as in Pi.
			agentDir := t.TempDir()
			cmd := exec.Command(bin, append([]string{"--mode", "rpc", "--no-session", "--offline", "--model", "test-faux/echo"}, tc.extra...)...)
			cmd.Dir = agentDir
			cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
			cmd.Stdin = stdin
			var stdoutBuf, stderrBuf bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdoutBuf, &stderrBuf
			if err := testenv.Run(t, cmd); err != nil {
				t.Fatalf("pig --mode rpc: %v\nstderr: %s", err, stderrBuf.String())
			}
			stdout := stdoutBuf.String()
			var order []string
			for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
				var record struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				}
				if json.Unmarshal([]byte(line), &record) == nil && record.Type == "response" {
					order = append(order, record.ID)
				}
			}
			if !slices.Equal(order, tc.want) {
				t.Fatalf("response order = %q, want %q\nstdout:\n%s", order, tc.want, stdout)
			}
		})
	}
}
