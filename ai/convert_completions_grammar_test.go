package ai

import (
	"encoding/json"
	"testing"
)

// TestConvertCompletionsMessagesReplaysGrammarToolCallsFromTheGivenProperties follows packages/ai/src/api/openai-completions.ts:1362:
// convertMessages replays a prior tool call as a custom (grammar) call when options.grammarToolInputProperties names an input
// property for that tool, sending the named argument as the custom input; a tool the map does not name stays a function call.
func TestConvertCompletionsMessagesReplaysGrammarToolCallsFromTheGivenProperties(t *testing.T) {
	transcript := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("patch it"), Timestamp: 1},
		AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonToolUse, Timestamp: 2, Content: []AssistantContentBlock{
			ToolCall{ID: "call-1", Name: "apply_patch", Arguments: JsonObject{"patch": "*** Begin Patch"}},
			ToolCall{ID: "call-2", Name: "ls", Arguments: JsonObject{"path": "."}},
		}},
		ToolResultMessage{ToolCallID: "call-1", ToolName: "apply_patch", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}, Timestamp: 3},
		ToolResultMessage{ToolCallID: "call-2", ToolName: "ls", Content: []ToolResultMessageContent{TextContent{Text: "a.txt"}}, Timestamp: 4},
	}})
	model := &Model{ID: "model", ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "custom", BaseURL: "https://example.test/v1"}, Input: []string{"text"}}
	type call struct {
		Type     string
		Function *struct{ Name, Arguments string }
		Custom   *struct{ Name, Input string }
	}
	calls := func(properties map[string]string) []call {
		t.Helper()
		converted, err := ConvertCompletionsMessages(model, transcript, nil, ConvertCompletionsMessagesOptions{GrammarToolInputProperties: properties})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(converted)
		if err != nil {
			t.Fatal(err)
		}
		var messages []struct {
			Role      string
			ToolCalls []call `json:"tool_calls"`
		}
		if err := json.Unmarshal(encoded, &messages); err != nil {
			t.Fatal(err)
		}
		for _, message := range messages {
			if message.Role == "assistant" {
				return message.ToolCalls
			}
		}
		t.Fatalf("no assistant message in %s", encoded)
		return nil
	}

	got := calls(map[string]string{"apply_patch": "patch"})
	if len(got) != 2 || got[0].Type != "custom" || got[0].Custom == nil || got[0].Custom.Name != "apply_patch" || got[0].Custom.Input != "*** Begin Patch" || got[0].Function != nil {
		t.Fatalf("grammar call = %+v, want a custom call carrying the patch argument", got)
	}
	if got[1].Type != "function" || got[1].Function == nil || got[1].Function.Name != "ls" || got[1].Custom != nil {
		t.Fatalf("other call = %+v, want a function call", got[1])
	}
	if plain := calls(nil); len(plain) != 2 || plain[0].Type != "function" || plain[0].Custom != nil {
		t.Fatalf("without the map (no declared grammar tool) = %+v, want function calls", plain)
	}
}
