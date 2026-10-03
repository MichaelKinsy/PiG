package coding

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's turn_end event hands its handlers the turn's ToolResultMessage objects (agent-session.ts
// _dispatchTurnEndBoundary), so JSON writes each as the session file does: a tool's details: null stays null and an
// absent details has no key.
func TestTurnEndToolResultsKeepDetailsAsPi(t *testing.T) {
	var seen []extension.ToolResultMessage
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(args ...any) (any, error) {
		seen = args[0].(extension.TurnEndEvent).ToolResults
		return nil, nil
	}}}}
	h := newBoundaryHarness(t, harnessOptions{extension: ext})
	now := time.Now().UnixMilli()
	assistant := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "c1", Name: "probe", Arguments: ai.JsonObject{}}, ai.ToolCall{ID: "c2", Name: "probe", Arguments: ai.JsonObject{}}}, Provider: "faux", ModelID: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: now, Usage: &ai.Usage{}}}
	results := []agent.ToolResultMessage{
		{Role: agent.RoleToolResult, ToolCallID: "c1", ToolName: "probe", Content: []ai.ToolResultMessageContent{}, DetailsNull: true, Timestamp: 1},
		{Role: agent.RoleToolResult, ToolCallID: "c2", ToolName: "probe", Content: []ai.ToolResultMessageContent{}, Timestamp: 2},
	}
	id, err := h.session.Inner().AppendMessage(assistant)
	if err != nil {
		t.Fatal(err)
	}
	h.session.rememberMessageEntry(assistant, id)
	for i := range results {
		if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{ToolResult: &results[i]}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.session.dispatchTurnEndBoundary(t.Context(), agent.TurnEndEvent{Message: assistant, ToolResults: results}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"details":null,"isError":false,"timestamp":1}`,
		`{"role":"toolResult","toolCallId":"c2","toolName":"probe","content":[],"isError":false,"timestamp":2}`,
	}
	if len(seen) != len(want) {
		t.Fatalf("turn_end toolResults %d, want %d", len(seen), len(want))
	}
	for i, message := range seen {
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != want[i] {
			t.Fatalf("turn_end toolResults[%d]\n got %s\nwant %s", i, encoded, want[i])
		}
	}
}
