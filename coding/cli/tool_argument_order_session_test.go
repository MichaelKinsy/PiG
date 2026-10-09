//go:build !pig_strip_mcp

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The session file keeps a tool call's arguments in the order the model wrote them: Pi appends JSON.stringify of the message
// whose toolCall.arguments is the object JSON.parse built (session-manager.ts appendMessage). The same call runs through a
// Node extension tool, so the tool, the provider's next request and the file all see one order.
func TestPersistedSessionKeepsTheModelsToolArgumentOrder(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home, cwd := t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const arguments = `{"zeta":"1","alpha":"a"}`
	chat := startScriptedChatServer(t, func(chatRequest) chatReply { return chatReply{Calls: [][2]string{{"audit_order", arguments}}} }, chatText("done"))
	models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
		"api": "openai-completions", "baseUrl": chat.URL, "apiKey": "fixture",
		"models": []any{map[string]any{"id": "test", "name": "Test", "contextWindow": 100000, "maxTokens": 4096}},
	}}})
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
		t.Fatal(err)
	}
	extension := filepath.Join(t.TempDir(), "audit-tools.mjs")
	if err := os.WriteFile(extension, []byte(auditDispatchExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := t.TempDir()
	cmd := exec.CommandContext(t.Context(), binary, "--no-extensions", "-e", extension, "--model", "fixture/test", "--no-skills", "--no-prompt-templates", "--no-context-files", "--session-dir", sessions, "--print", "go")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
	cmd.Stdin = strings.NewReader("")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("print mode: %v\n%s", err, out)
	}
	files, _ := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files = %v, want one", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for line := range strings.SplitSeq(string(data), "\n") {
		var entry struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		var blocks []struct {
			Type      string          `json:"type"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(entry.Message.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type == "toolCall" {
				found = true
				if string(block.Arguments) != arguments {
					t.Errorf("session wrote arguments %s, want %s", block.Arguments, arguments)
				}
			}
		}
	}
	if !found {
		t.Fatalf("no tool call in the session file:\n%s", data)
	}
}
