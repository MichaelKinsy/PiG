package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// main.ts:707-714 appends --name to the session manager of a new Session before createAgentSessionRuntime (main.ts:863), and createAgentSession then saves the initial model and thinking level (sdk.ts:452-458), so session_info is the first entry of the RPC Session's file. Pi 1.1.0, probed with --mode rpc --session-dir --name and one prompt: session, session_info, model_change, thinking_level_change, then the messages.
func TestRPCStartupNamePrecedesTheInitialModelEntries(t *testing.T) {
	home := t.TempDir()
	sessions := filepath.Join(home, "sessions")
	p := startRPCProcessAt(t, t.TempDir(), []string{
		"HOME=" + home, "USERPROFILE=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1",
	}, "--model", "test-faux/faux-1", "--session-dir", sessions, "--name", "Named")
	p.send(`{"id":"p","type":"prompt","message":"hello"}`)
	p.await("the prompt's agent_end", func(record rpcRecord) bool { return record["type"] == "agent_end" })
	p.closeAndWait("the prompt ended")
	files, err := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("session files = %v, %v; want one", files, err)
	}
	file, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var types []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var entry struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Type == "session_info" && entry.Name != "Named" {
			t.Fatalf("session_info name = %q", entry.Name)
		}
		types = append(types, entry.Type)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"session", "session_info", "model_change", "thinking_level_change"}; len(types) < len(want) || !slices.Equal(types[:len(want)], want) {
		t.Fatalf("session entries = %v, want them to start with %v", types, want)
	}
}
