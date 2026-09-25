package agent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// An assistant message carrying a tool call must survive a marshal/unmarshal
// round trip. Sessions persist AgentMessage via MarshalJSON; if the emitted
// content-block discriminator is not one the decoder accepts, every persisted
// tool-calling turn becomes unreadable on resume (observed: 13,524 of 15,942
// message entries in a real session failing with `unknown type "tool_use"`).
func TestAgentMessageToolCallRoundTrip(t *testing.T) {
	original := AgentMessage{Assistant: &AssistantMessage{
		Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "calling a tool"},
			ai.ToolCall{
				ID:        "call_1",
				Name:      "bash",
				Arguments: ai.JsonObject{"command": "echo hi"},
			},
		},
	}}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded AgentMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round trip failed: %v\nwire: %s", err, encoded)
	}
	if decoded.Assistant == nil {
		t.Fatalf("assistant variant lost: %s", encoded)
	}
	if got := len(decoded.Assistant.Content); got != 2 {
		t.Fatalf("content blocks = %d, want 2: %s", got, encoded)
	}
	tu, ok := decoded.Assistant.Content[1].(ai.ToolCall)
	if !ok {
		t.Fatalf("block[1] = %T, want ai.ToolCall", decoded.Assistant.Content[1])
	}
	if tu.ID != "call_1" || tu.Name != "bash" {
		t.Fatalf("tool call mangled: %+v", tu)
	}
}
