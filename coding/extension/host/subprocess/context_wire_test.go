package subprocess

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The context event's encoding is its messages encoded one by one and joined; it is the bytes json.Marshal gives the event, for every message variant and the characters the encoder escapes.
func TestContextEventWireIsWhatMarshalGives(t *testing.T) {
	messages := []extension.AgentMessage{
		wireMessage(map[string]any{"role": "system", "content": "<system> & \u2028", "timestamp": 0, "toolsAdded": []any{}}),
		wireMessage(map[string]any{"role": "user", "content": "a <b> & c \u2029 é 😀", "timestamp": 1}),
		{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "<tag>"}}, Timestamp: 2}},
		{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "x&y"}, ai.ToolCall{ID: "c1", Name: "read", Arguments: ai.JsonObject{"path": "<f>", "n": 1.5}}}, StopReason: ai.StopReasonToolUse, Timestamp: 3}},
		{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Timestamp: 4}},
	}
	for name, list := range map[string][]extension.AgentMessage{"messages": messages, "none": nil, "empty": {}, "one": messages[1:2]} {
		for _, event := range []any{
			extension.ContextEvent{Type: "context", Messages: list},
			extension.ContextWithSystemEvent{Type: "context_with_system", Messages: list},
		} {
			want, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			got, err := marshalEventWire(event)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s %T:\n got %s\nwant %s", name, event, got, want)
			}
		}
	}
}
