package ai

import "testing"

// Mirrors the upstream openai-completions.ts:1260 contract
// (hasText ? textResult : hasImages ? "(see attached image)" : "(no tool output)").
// An empty tool result with no images (any command that produced no stdout) must
// send the "(no tool output)" placeholder, never an empty tool message: some
// backends reject empty tool content and the model loses the turn signal.
func TestToolResultEmpty_NoOutputPlaceholder(t *testing.T) {
	messages := []Message{ToolResultMessage{
		ToolCallID: "call_1",
		Content:    []ToolResultMessageContent{TextContent{}},
	}}

	out, err := convertMessages(messages, false, nil)
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 tool message, got %d", len(out))
	}
	if s, ok := out[0].Content.(string); !ok || s != "(no tool output)" {
		t.Fatalf("empty tool result must use the (no tool output) placeholder, got %#v", out[0].Content)
	}
}
