package ai

import (
	"reflect"
	"testing"
)

// transform-messages.ts:131-139 applies normalizeToolCallId only when `!isSameModel`, so a call the same model produced keeps its id and its result is not
// remapped, also when the message still needs the block-by-block pass (here an empty thinking block that the pass drops, which the all-unchanged fast
// path does not take): the callback runs for the other model's call only.
// mutation-checked: running the callback for a same-model message that takes the block-by-block pass (the `!sameModel` condition around the call
// normalization in transformMessages) renames the same-model id and fails it.
func TestTransformMessagesNormalizesOnlyForeignToolCallIDsOnTheBlockPass(t *testing.T) {
	model := &Model{ID: "m", ProviderMeta: ProviderMetadata{ProviderID: "p", API: APIOpenAICompletions}}
	const sameID, foreignID = "same|id+/=", "foreign|id+/="
	call := func(id string) ToolCall { return ToolCall{ID: id, Name: "read", Arguments: JsonObject{}} }
	result := func(id string) ToolResultMessage {
		return ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}}
	}
	messages := []Message{
		UserMessage{Content: UserText("go")},
		AssistantMessage{Provider: "p", API: APIOpenAICompletions, Model: "m", StopReason: StopReasonToolUse,
			Content: []AssistantContentBlock{ThinkingContent{Thinking: ""}, call(sameID)}},
		result(sameID),
		AssistantMessage{Provider: "q", API: APIAnthropicMessages, Model: "other", StopReason: StopReasonToolUse,
			Content: []AssistantContentBlock{call(foreignID)}},
		result(foreignID),
	}
	var seen []string
	out := TransformMessages(messages, model, func(id string, _ *Model, _ AssistantMessage) string {
		seen = append(seen, id)
		return "n_" + id
	})
	if !reflect.DeepEqual(seen, []string{foreignID}) {
		t.Fatalf("normalizeToolCallId ran for %q, want only the other model's call", seen)
	}
	var calls, results []string
	for _, message := range out {
		switch typed := message.(type) {
		case AssistantMessage:
			for _, block := range typed.Content {
				if tc, ok := block.(ToolCall); ok {
					calls = append(calls, tc.ID)
				}
			}
		case ToolResultMessage:
			results = append(results, typed.ToolCallID)
		}
	}
	want := []string{sameID, "n_" + foreignID}
	if !reflect.DeepEqual(calls, want) || !reflect.DeepEqual(results, want) {
		t.Fatalf("tool call ids %q, result ids %q, want both %q", calls, results, want)
	}
}
