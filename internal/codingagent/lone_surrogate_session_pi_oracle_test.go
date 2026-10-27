package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A message that holds a lone UTF-16 surrogate is written to the session file as Pi writes it: Pi's appendMessage writes
// JSON.stringify(entry), which writes the unit as the escape `\ud83d` and leaves `<`, `>` and `&` as they are. The oracle is
// Node's own JSON.stringify(JSON.parse(line)): a line PiG wrote must equal it, byte for byte, and hold the escape in the user
// text, the assistant text, a tool call's arguments, the tool result's text and its details.
func TestSessionLinesWithALoneSurrogateAreWhatPisJSONStringifyWrites(t *testing.T) {
	const wtf = "a\xed\xa0\xbd<&>"
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-lone-surrogate", "")
	if err != nil {
		t.Fatal(err)
	}
	user := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: wtf}}, Timestamp: time.Now().UnixMilli()}}
	assistant := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Timestamp: time.Now().UnixMilli(),
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: wtf}, ai.ToolCall{ID: "c1", Name: "tool", Arguments: ai.JsonObject{"arg": wtf, "list": []any{wtf}}}}}}
	result := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c1", ToolName: "tool",
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: wtf}}, Details: map[string]any{"d": wtf, "n": []any{wtf}}, Timestamp: time.Now().UnixMilli()}}
	for _, message := range []agent.AgentMessage{user, assistant, result} {
		if _, err := sess.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("%d lines in %s", len(lines), data)
	}
	input, err := json.Marshal(lines)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(testenv.ModuleRoot(t), "ai", "testdata", "stringify_roundtrip_oracle.mjs"))
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi's JSON: %v\n%s", err, &stderr)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, line := range lines {
		if line != want[i] {
			t.Errorf("line %d differs from JSON.stringify(JSON.parse(line)):\n got  %s\n Pi's %s", i, line, want[i])
		}
		if i > 0 && (strings.Contains(line, "\ufffd") || strings.Count(line, `\ud83d`) == 0) {
			t.Errorf("line %d lost the lone surrogate: %s", i, line)
		}
	}
}
