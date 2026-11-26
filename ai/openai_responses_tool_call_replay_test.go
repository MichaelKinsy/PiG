package ai

import (
	"encoding/json"
	"testing"
)

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

	// Verify exact JSON field order matches Pi replayedCall literal:
	// type, id, call_id, name, status, namespace (when present), arguments/input.
	fnBytes, err := json.Marshal(gotFn)
	if err != nil {
		t.Fatalf("marshal function_call: %v", err)
	}
	wantFnJSON := `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","status":"completed","arguments":"{\"path\":\"calc.py\"}"}`
	if string(fnBytes) != wantFnJSON {
		t.Fatalf("function_call JSON =\n got: %s\nwant: %s", fnBytes, wantFnJSON)
	}
	customBytes, err := json.Marshal(gotCustom)
	if err != nil {
		t.Fatalf("marshal custom_tool_call: %v", err)
	}
	wantCustomJSON := `{"type":"custom_tool_call","id":"ctc_2","call_id":"call_2","name":"codemode","status":"completed","input":"text(1)"}`
	if string(customBytes) != wantCustomJSON {
		t.Fatalf("custom_tool_call JSON =\n got: %s\nwant: %s", customBytes, wantCustomJSON)
	}
}
