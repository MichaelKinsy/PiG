package ai

import (
	"encoding/json"
	"errors"
	"testing"
)

// Pi anthropic-messages.ts:1236-1262: params.thinking.display is options.thinkingDisplay ?? "summarized" for managed-effort, adaptive and budget thinking.
func TestAnthropicThinkingDisplayOptionRequestShapes(t *testing.T) {
	capture := func(t *testing.T, cfg AnthropicConfig, options StreamOptions) map[string]any {
		t.Helper()
		cfg.APIKey, cfg.BaseURL = "test-key", "http://127.0.0.1:9"
		provider := NewAnthropicProvider(cfg)
		defer func() { _ = provider.Close() }()
		var thinking map[string]any
		options.CacheRetention = CacheRetentionNone
		options.OnPayload = func(value any, _ *Model) (any, error) {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			var payload struct {
				Thinking map[string]any `json:"thinking"`
			}
			if err := json.Unmarshal(encoded, &payload); err != nil {
				return nil, err
			}
			thinking = payload.Thinking
			return nil, errors.New("payload captured")
		}
		stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}}), options)
		if err != nil {
			t.Fatal(err)
		}
		if result := stream.Result(); result.ErrorMessage != "payload captured" {
			t.Fatalf("payload not captured: %+v", result)
		}
		return thinking
	}
	adaptive := AnthropicConfig{Model: "claude-fable-5-1", ProviderID: "anthropic", Compat: &AnthropicMessagesCompat{ForceAdaptiveThinking: new(true)}, ModelMetadata: &Model{ID: "claude-fable-5-1", Input: []string{"text"}, Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelMax, ContextWindow: 200000, MaxOutputTokens: 32000}, ProviderMeta: ProviderMetadata{ProviderID: "anthropic", API: APIAnthropicMessages, Reasoning: true}}}
	managed := adaptive
	managed.Compat = &AnthropicMessagesCompat{ForceAdaptiveThinking: new(true), SupportsMidConvoEffort: new(true)}
	budget := AnthropicConfig{Model: "claude-sonnet-4-0", ProviderID: "anthropic", ModelMetadata: &Model{ID: "claude-sonnet-4-0", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 32000}, ProviderMeta: ProviderMetadata{ProviderID: "anthropic", API: APIAnthropicMessages, Reasoning: true}}}
	for _, test := range []struct {
		name    string
		config  AnthropicConfig
		display AnthropicThinkingDisplay
		want    string
		kind    string
	}{
		{"adaptive default", adaptive, "", "summarized", "adaptive"},
		{"adaptive omitted", adaptive, AnthropicThinkingDisplayOmitted, "omitted", "adaptive"},
		{"managed effort omitted", managed, AnthropicThinkingDisplayOmitted, "omitted", "adaptive"},
		{"managed effort default", managed, "", "summarized", "adaptive"},
		{"budget default", budget, "", "summarized", "enabled"},
		{"budget omitted", budget, AnthropicThinkingDisplayOmitted, "omitted", "enabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			thinking := capture(t, test.config, StreamOptions{ThinkingEnabled: new(true), ThinkingDisplay: test.display})
			if thinking["type"] != test.kind || thinking["display"] != test.want {
				t.Fatalf("thinking=%v, want type %s display %s", thinking, test.kind, test.want)
			}
		})
	}
}
