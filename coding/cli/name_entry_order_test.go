package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pi names a new Session on its SessionManager before it creates the runtime (main.ts:707-714 appendSessionInfo, then main.ts:863 createAgentSessionRuntime), and the runtime then records the start model and thinking level (sdk.ts:453-458). The session file of `--name` therefore lists session_info before model_change and thinking_level_change in print and JSON mode, as TestRPCStartupNamePrecedesTheInitialModelEntries pins for RPC.
// mutation-checked: appending the name after the runtime exists, as each mode did, writes model_change, thinking_level_change, session_info.
func TestNameIsTheNewSessionsFirstEntry(t *testing.T) {
	want := []string{"session", "session_info", "model_change", "thinking_level_change", "message"}
	env := []string{"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic"}
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			home, dir := t.TempDir(), t.TempDir()
			env := append(slices.Clone(env), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR=")
			args := []string{"--offline", "--no-extensions", "--name", "named", "--session-dir", dir, "--model", "test-faux/faux-1"}
			cmd := exec.CommandContext(t.Context(), buildPigBinaryForSignalTest(t), append(args, "--mode", mode, "-p", "What is 20+22?")...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), env...)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("named turn failed: %v: %s", err, output)
			}
			files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
			if err != nil || len(files) != 1 {
				t.Fatalf("session files = %v, error = %v", files, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var types []string
			for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
				var entry struct{ Type string }
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					t.Fatal(err)
				}
				types = append(types, entry.Type)
			}
			if len(types) < len(want) || !slices.Equal(types[:len(want)], want) {
				t.Fatalf("session entries = %v, want them to start with %v", types, want)
			}
		})
	}
}
