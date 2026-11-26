package ai

import "testing"

// Port of Pi main 69a6993486acee850d14a4a1547daa4a167228ad (#12367).
func TestOpenAIResponsesReplaysToolCallsWithStatusCompleted(t *testing.T) {
	model := &Model{
		ID: "gpt-6.2-sol",
		ProviderMeta: ProviderMetadata{
			ProviderID: "openai-codex",
			API:        APIOpenAICodexResponses,
		},
	}
	assistant := AssistantMessage{
		Content: []AssistantContentBlock{
			ToolCall{ID: "call_1|fc_1", Name: "read", Arguments: JsonObject{"path": "calc.py"}},
			ToolCall{ID: "call_2|ctc_2", Name: "codemode", Arguments: JsonObject{"code": "text(1)"}},
		},
		API:        model.ProviderMeta.API,
		Provider:   model.ProviderMeta.ProviderID,
		Model:      model.ID,
		StopReason: StopReasonToolUse,
		Timestamp:  1,
	}
	ctx := NormalizeContext(Context{
		Messages: []Message{
			UserMessage{Content: UserText("Use the tools."), Timestamp: 1},
			assistant,
			ToolResultMessage{ToolCallID: "call_1|fc_1", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}, Timestamp: 2},
			ToolResultMessage{ToolCallID: "call_2|ctc_2", ToolName: "codemode", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}, Timestamp: 3},
		},
	})

	items, err := ConvertResponsesMessages(model, ctx, map[string]bool{"openai-codex": true}, ConvertResponsesMessagesOptions{
		GrammarToolInputProperties: map[string]string{"codemode": "code"},
	})
	if err != nil {
		t.Fatalf("ConvertResponsesMessages failed: %v", err)
	}

	var gotFn, gotCustom *ResponsesInputItem
	for i := range items {
		switch items[i].Type {
		case "function_call":
			gotFn = &items[i]
		case "custom_tool_call":
			gotCustom = &items[i]
		}
	}
	if gotFn == nil || gotFn.ID != "fc_1" || gotFn.CallID != "call_1" || gotFn.Name != "read" || gotFn.Status != "completed" {
		t.Fatalf("function_call = %#v, want status=completed", gotFn)
	}
	if gotCustom == nil || gotCustom.ID != "ctc_2" || gotCustom.CallID != "call_2" || gotCustom.Name != "codemode" || gotCustom.Input != "text(1)" || gotCustom.Status != "completed" {
		t.Fatalf("custom_tool_call = %#v, want status=completed", gotCustom)
	}
}
