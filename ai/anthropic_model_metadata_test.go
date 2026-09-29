package ai

import (
	"encoding/json"
	"testing"
)

// upstream: packages/ai/src/api/anthropic-messages.ts:838-885,1035-1073,1159-1182 reads the supplied model, including models absent from the built-in catalog.
func TestAnthropicSuppliedModelThinkingMetadata(t *testing.T) {
	t.Parallel()
	model := &Model{ID: "new-reasoner", ProviderMeta: ProviderMetadata{ProviderID: "custom-messages", Reasoning: true, Compat: &ModelCompat{ForceAdaptiveThinking: new(true)}}, Capabilities: ModelCapabilities{MaxOutputTokens: 12345, MaxThinking: ThinkingMax}, ThinkingLevelMap: ThinkingLevelMap{ThinkingMax: new("low")}}
	provider := &anthropicProvider{cfg: AnthropicConfig{Model: model.ID, ProviderID: model.ProviderMeta.ProviderID, ModelMetadata: model}}
	params, err := provider.buildParams(provider.resolveModel(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("test"), Timestamp: 0}}}), false, anthropicHeaders{}, anthropicHeaders{}, StreamOptions{Thinking: ThinkingMax}, nil)
	if err != nil {
		t.Fatal(err)
	}
	thinking, err := json.Marshal(params.request.Thinking)
	if err != nil {
		t.Fatal(err)
	}
	assertShapeJSON(t, thinking, `{"type":"adaptive","display":"summarized"}`)
	if params.request.OutputConfig == nil || params.request.OutputConfig.Effort != "low" || params.request.MaxTokens != 12345 {
		t.Fatalf("request = %+v; want supplied native effort low and max_tokens 12345", params.request)
	}
}
