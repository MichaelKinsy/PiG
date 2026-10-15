package agent

// pi: packages/coding-agent/src/core/messages.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// .upstream/current/packages/coding-agent/src/core/messages.ts:148 convertToLlm(messages) only converts roles: it takes one
// argument and neither drops an errored assistant turn nor adds a tool result for an orphaned call (those are the
// provider-layer transformMessages, NormalizeMessages here).
func TestConvertToLLMOnlyConvertsAndNormalizeMessagesFilters(t *testing.T) {
	msgs := []AgentMessage{
		{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}},
		{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: "error", ErrorMessage: "boom", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial"}}}},
		{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: "toolUse", Content: []ai.AssistantContentBlock{
			ai.ToolCall{ID: "call_1", Name: "read", Arguments: ai.JsonObject{"path": "x.go"}},
		}}},
		{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "next"}}}},
	}
	if got := ConvertToLLM(msgs); len(got) != 4 {
		t.Fatalf("ConvertToLLM kept %d messages, want all 4 converted: %#v", len(got), got)
	}
	// user + tool-call assistant + synthetic tool result + user; the errored turn is gone.
	if got := ConvertToLLM(NormalizeMessages(msgs, nil)); len(got) != 4 {
		t.Fatalf("normalized conversion has %d messages, want 4: %#v", len(got), got)
	}
	normalized := ConvertToLLM(NormalizeMessages(msgs, nil))
	if _, ok := normalized[2].(ai.ToolResultMessage); !ok {
		t.Fatalf("message 2 of the normalized conversion is %T, want the synthetic tool result", normalized[2])
	}
	if _, ok := ConvertToLLM(msgs)[2].(ai.ToolResultMessage); ok {
		t.Fatal("ConvertToLLM synthesized a tool result")
	}
}
