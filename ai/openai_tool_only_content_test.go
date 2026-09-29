package ai

import "testing"

// Pi api/openai-completions.ts:1283-1288 uses null assistant content unless the provider explicitly requires an empty string.
func TestToolOnlyAssistantContentUsesNullUnlessRequired(t *testing.T) {
	for _, requires := range []bool{false, true} {
		p := NewOpenAIProvider(OpenAIConfig{Model: "test", Compat: &OpenAICompat{RequiresAssistantAfterToolResult: new(requires)}}).(*openAIProvider)
		messages, err := p.convertMessagesWithCompat([]Message{AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "call", Name: "read", Arguments: JsonObject{}}}}}, nil, "system", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 1 {
			t.Fatalf("messages = %#v", messages)
		}
		var want any
		if requires {
			want = ""
		}
		if messages[0].Content != want {
			t.Fatalf("requires=%t content=%#v want %#v", requires, messages[0].Content, want)
		}
	}
}
