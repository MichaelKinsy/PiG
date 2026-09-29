package ai

import "testing"

// Pi openai-responses-shared.ts:163-174,255-258,294-326 derives replay identity from provider/API/model metadata, never from the stored item's prefix.
func TestResponsesReplayIdentityUsesSourceMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, sourceProvider, sourceModel, id, wantID, wantCallID, wantNamespace string
		api                                                                      API
		grammar                                                                  bool
	}{
		{"same model custom", "openai", "target", "call_1|ctc_1", "ctc_1", "call_1", "functions", APIOpenAIResponses, true},
		{"same model function", "openai", "target", "call_1|fc_native", "fc_native", "call_1", "functions", APIOpenAIResponses, false},
		{"same provider different model", "openai", "old", "call_1|fc_native", "", "call_1", "", APIOpenAIResponses, false},
		// Pi probe: shortHash("fc_native") produces otg7yamm1iir.
		{"foreign item already has fc prefix", "other", "source", "call 1|fc_native", "fc_otg7yamm1iir", "call_1", "", APIOpenAICompletions, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "target"}}
			messages := []Message{AssistantMessage{Provider: tc.sourceProvider, Model: tc.sourceModel, API: tc.api, StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: tc.id, Name: "sample_tool", Arguments: JsonObject{"payload": "abc"}, Namespace: "functions"}}}, ToolResultMessage{ToolCallID: tc.id, ToolName: "sample_tool", Content: []ToolResultMessageContent{TextContent{Text: "done"}}}}
			var props map[string]string
			if tc.grammar {
				props = map[string]string{"sample_tool": "payload"}
			}
			items, err := provider.convertMessages(messages, props)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 2 || items[0].ID != tc.wantID || items[0].CallID != tc.wantCallID || items[1].CallID != tc.wantCallID || items[0].Namespace != tc.wantNamespace {
				t.Fatalf("items=%#v", items)
			}
		})
	}
}

func TestAzureResponsesReplayUsesLogicalModelIdentity(t *testing.T) {
	provider := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{ProviderID: "custom-azure", Model: "logical-model", AzureDeploymentName: "deployment", BaseURL: "https://example.test", APIKey: "test"}).(*openAIResponsesProvider)
	messages := []Message{AssistantMessage{Provider: "custom-azure", API: APIAzureOpenAIResponses, Model: "logical-model", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "call_1|ctc_1", Name: "sample_tool", Arguments: JsonObject{"payload": "abc"}, Namespace: "functions"}}}}
	items, err := provider.convertMessages(messages, map[string]string{"sample_tool": "payload"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "ctc_1" || items[0].Namespace != "functions" {
		t.Fatalf("items=%#v", items)
	}
}
