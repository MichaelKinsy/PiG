package ai

import (
	"encoding/json"
	"errors"
	"testing"
)

// captureCatalogPayload uses the provider's production request path and aborts at
// the same onPayload boundary as the upstream catalog tests. No network is used.
func captureCatalogPayload(t *testing.T, provider Provider, opts StreamOptions) map[string]any {
	t.Helper()
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	captured := errors.New("payload captured")
	var payload map[string]any
	opts.OnPayload = func(value any, _ *Model) (any, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		return nil, captured
	}
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserText("Hello")}}}), opts)
	if err != nil && !errors.Is(err, captured) {
		t.Fatal(err)
	}
	if stream != nil {
		_ = stream.Result()
	}
	if payload == nil {
		t.Fatal("onPayload was not called")
	}
	return payload
}

func TestMaxThinkingUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/ai/test/max-thinking.test.ts:15
	t.Run("is opt-in for ordinary reasoning models", func(t *testing.T) {
		model := &Model{ID: "ordinary-reasoning", DisplayName: "Ordinary Reasoning", Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh, ContextWindow: 128000, MaxOutputTokens: 4096}, Input: []string{"text"}, ProviderMeta: ProviderMetadata{ProviderID: "test", API: APIOpenAICompletions, BaseURL: "https://example.com/v1", Reasoning: true}}
		assertCatalogJSON(t, GetSupportedThinkingLevels(model), `["off","minimal","low","medium","high"]`)
		if got := ClampThinkingLevel(model, ThinkingMax); got != ThinkingHigh {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/max-thinking.test.ts:33
	for _, id := range []string{"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-6-luna", "gpt-6-sol"} {
		t.Run("exposes xhigh and max for openai-codex/"+id, func(t *testing.T) {
			m := mustGeneratedModel(t, "openai-codex", id)
			assertThinkingLevelMap(t, m, map[ThinkingLevel]string{ThinkingXHigh: "xhigh", ThinkingMax: "max"})
			assertCatalogJSON(t, GetSupportedThinkingLevels(m.ToModel()), `["off","minimal","low","medium","high","xhigh","max"]`)
		})
	}
	// .upstream/v0.99.1/packages/ai/test/max-thinking.test.ts:51
	t.Run("supports a hole between high and max", func(t *testing.T) {
		model := &Model{ID: "high-and-max", DisplayName: "High and Max", Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh, ContextWindow: 128000, MaxOutputTokens: 4096}, Input: []string{"text"}, ProviderMeta: ProviderMetadata{ProviderID: "test", API: APIOpenAICompletions, BaseURL: "https://example.com/v1", Reasoning: true}, ThinkingLevelMap: ThinkingLevelMap{ThinkingXHigh: nil, ThinkingMax: new("max")}}
		assertCatalogJSON(t, GetSupportedThinkingLevels(model), `["off","minimal","low","medium","high","max"]`)
		if got := ClampThinkingLevel(model, ThinkingXHigh); got != ThinkingMax {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/max-thinking.test.ts:70
	for _, id := range []string{"gpt-5.6-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol"} {
		t.Run("sends max to the Codex Responses API for "+id, func(t *testing.T) {
			m := mustGeneratedModel(t, "openai-codex", id)
			p := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "acc_test"), Model: m.ID, ProviderID: m.Provider, BaseURL: m.BaseURL, Compat: m.Compat})
			payload := captureCatalogPayload(t, p, StreamOptions{Thinking: ThinkingMax, IsReasoning: m.Reasoning, Transport: TransportSSE})
			assertCatalogJSON(t, payload["reasoning"], `{"effort":"max","summary":"auto"}`)
		})
	}
}
