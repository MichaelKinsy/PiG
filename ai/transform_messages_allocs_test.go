package ai

import (
	"fmt"
	"testing"
)

func transformHistory(turns int) []Message {
	messages := make([]Message, 0, turns*5)
	for turn := range turns {
		messages = append(messages, UserMessage{Content: UserText(fmt.Sprintf("prompt %d", turn))})
		calls := []AssistantContentBlock{
			ToolCall{ID: fmt.Sprintf("a%d", turn), Name: "read", Arguments: JsonObject{}},
			ToolCall{ID: fmt.Sprintf("b%d", turn), Name: "read", Arguments: JsonObject{}},
			ToolCall{ID: fmt.Sprintf("c%d", turn), Name: "read", Arguments: JsonObject{}},
		}
		messages = append(messages, AssistantMessage{API: APIAnthropicMessages, Provider: "anthropic", Model: "m", Content: calls, StopReason: StopReasonToolUse})
		for _, id := range []string{"a", "b", "c"} {
			messages = append(messages, ToolResultMessage{ToolCallID: fmt.Sprintf("%s%d", id, turn), ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "result"}}})
		}
	}
	return messages
}

// TransformMessages keeps a message that needs no change as the interface value it arrived as; boxing the copy of every user, system and tool result message would allocate the whole history again on every request. A model without image input changes only content that holds an image, so a text-only history stays unchanged for it too. The bound is the measured cost with headroom: a history of 100 turns (500 messages) allocates the transformed slice and the tool-flow bookkeeping, and no copy of a message that needs no change.
func TestTransformMessagesDoesNotReboxUnchangedMessages(t *testing.T) {
	for _, input := range [][]string{{"text", "image"}, {"text"}} {
		model := &Model{ID: "m", Input: input, ProviderMeta: ProviderMetadata{ProviderID: "anthropic", API: APIAnthropicMessages}}
		messages := transformHistory(100)
		allocs := testing.AllocsPerRun(20, func() { _ = TransformMessages(messages, model, nil) })
		if limit := 20.0; allocs > limit {
			t.Fatalf("input %v: TransformMessages allocates %.0f times for 500 messages, want at most %.0f", input, allocs, limit)
		}
		t.Logf("input %v: allocations for 500 messages: %.0f", input, allocs)
	}
}
