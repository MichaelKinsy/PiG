package cli

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// JSON mode and RPC mode write each event as JSON.stringify(event) and a newline. An event that carries a message with a
// lone UTF-16 surrogate (a model's text, a tool call's arguments, a tool result's text or details) must have the unit as the
// escape `\ud83d` and `<`, `>` and `&` as they are. The oracle is Node's own JSON.stringify(JSON.parse(line)).
func TestJSONLinesOfAMessageWithALoneSurrogateAreWhatPisJSONStringifyWrites(t *testing.T) {
	const wtf = "a\xed\xa0\xbd<&>"
	now := time.Now().UnixMilli()
	messages := map[string]agent.AgentMessage{
		"assistant": {Assistant: &agent.AssistantMessage{Role: "assistant", Timestamp: now,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: wtf}, ai.ToolCall{ID: "c1", Name: "tool", Arguments: ai.JsonObject{"arg": wtf, "list": []any{wtf}}}}}},
		"toolResult": {ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c1", ToolName: "tool",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: wtf}}, Details: map[string]any{"d": wtf, "n": []any{wtf}}, Timestamp: now}},
	}
	var lines []string
	for _, name := range []string{"assistant", "toolResult"} {
		var out bytes.Buffer
		writeJSONLine(&out, map[string]any{"type": "message_end", "message": messages[name]})
		lines = append(lines, strings.TrimSuffix(out.String(), "\n"))
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
		if strings.Contains(line, "\ufffd") || strings.Count(line, `\ud83d`) == 0 {
			t.Errorf("line %d lost the lone surrogate: %s", i, line)
		}
	}
}
