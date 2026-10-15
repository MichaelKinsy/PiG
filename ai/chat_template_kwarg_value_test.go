package ai

import "testing"

// Pi packages/ai/src/types.ts:90 ChatTemplateKwargValue (a string, number, boolean or null, or a `{ $var }` placeholder) and :826
// chatTemplateKwargs?: Record<string, ChatTemplateKwargValue>: every literal kind reaches chat_template_kwargs unchanged and a
// `$var` placeholder resolves from the request's thinking state.
func TestChatTemplateKwargValueKindsReachThePayload(t *testing.T) {
	registerTestModel(t, GeneratedModel{ID: "kwarg-model", Provider: "custom", Reasoning: true, MaxOutputTokens: 4096, ThinkingLevelMap: map[ModelThinkingLevel]*string{ThinkingHigh: new("high")}})
	kwargs := map[string]ChatTemplateKwargValue{
		"text": "kept", "number": 7.5, "flag": true, "nothing": nil,
		"effort": map[string]any{"$var": "thinking.effort"},
	}
	request := captureOpenAIRequestMap(t, "custom", "kwarg-model", &OpenAICompat{ThinkingFormat: "chat-template", ChatTemplateKwargs: kwargs},
		StreamOptions{IsReasoning: true, Thinking: ThinkingLevelHigh})
	got, ok := request["chat_template_kwargs"].(map[string]any)
	if !ok || got["text"] != "kept" || got["number"] != 7.5 || got["flag"] != true || got["effort"] != "high" {
		t.Fatalf("chat_template_kwargs = %#v", request["chat_template_kwargs"])
	}
	if value, present := got["nothing"]; !present || value != nil {
		t.Fatalf("a null kwarg must stay null, got %#v present=%v", value, present)
	}
}
