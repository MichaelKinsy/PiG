package ai

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func upstreamCatalogModel(t *testing.T, provider, id string) *Model {
	t.Helper()
	generated, ok := LookupModelExact(provider + "/" + id)
	if !ok {
		t.Fatalf("missing model %s/%s", provider, id)
	}
	return generated.ToModel()
}

func upstreamAnthropicParams(t *testing.T, model *Model, ctx Context, opts StreamOptions) map[string]json.RawMessage {
	t.Helper()
	provider := NewAnthropicProvider(AnthropicConfig{Model: model.ID, ProviderID: model.ProviderMeta.ProviderID, ModelMetadata: model, BaseURL: "http://127.0.0.1:9", APIKey: "fake-key", Compat: model.ProviderMeta.Compat})
	return captureAnthropicUpstreamPayload(t, provider, ctx, opts)
}

func requireAbsentJSON(t *testing.T, payload map[string]json.RawMessage, key string) {
	t.Helper()
	if value, ok := payload[key]; ok {
		t.Errorf("%s must be absent, got %s", key, value)
	}
}

func customAdaptiveModel(compat *ModelCompat) *Model {
	return &Model{ID: "vendor--claude-opus-latest", DisplayName: "Vendor Proxy Opus Latest", Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh, ContextWindow: 200000, MaxOutputTokens: 32000}, Input: []string{"text"}, ProviderMeta: ProviderMetadata{ProviderID: "vendor-proxy", API: APIAnthropicMessages, BaseURL: "http://127.0.0.1:9", Reasoning: true, Compat: compat}}
}

// Ports packages/ai/test/anthropic-force-adaptive-thinking.test.ts:71-122 through the same streamSimple caller and onPayload boundary.
func TestAnthropicUpstreamForceAdaptiveThinking(t *testing.T) {
	ctx := Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}
	optOut := upstreamCatalogModel(t, "anthropic", "claude-opus-4-8")
	optOut.ProviderMeta.Compat = &ModelCompat{ForceAdaptiveThinking: new(false)}
	for _, tc := range []struct {
		name             string
		model            *Model
		level            ModelThinkingLevel
		thinking, output string
	}{
		// .upstream/v0.87.1/packages/ai/test/anthropic-force-adaptive-thinking.test.ts:71
		{"sends legacy thinking payload for custom model ids by default", customAdaptiveModel(nil), ThinkingMedium, "enabled", ""},
		// .upstream/v0.87.1/packages/ai/test/anthropic-force-adaptive-thinking.test.ts:78
		{"sends adaptive thinking payload when compat.forceAdaptiveThinking is true", customAdaptiveModel(&ModelCompat{ForceAdaptiveThinking: new(true)}), ThinkingMedium, "adaptive", "medium"},
		// .upstream/v0.87.1/packages/ai/test/anthropic-force-adaptive-thinking.test.ts:85
		{"uses adaptive thinking with native xhigh effort for Claude Fable 5", upstreamCatalogModel(t, "anthropic", "claude-fable-5"), ThinkingXHigh, "adaptive", "xhigh"},
		// .upstream/v0.87.1/packages/ai/test/anthropic-force-adaptive-thinking.test.ts:92 (all three table rows)
		{"uses adaptive thinking effort without a token budget for Kimi Coding kimi-for-coding", upstreamCatalogModel(t, "kimi-coding", "kimi-for-coding"), ThinkingMedium, "adaptive", "medium"},
		{"uses adaptive thinking effort without a token budget for Kimi Coding k3", upstreamCatalogModel(t, "kimi-coding", "k3"), ThinkingMax, "adaptive", "max"},
		{"uses adaptive thinking effort without a token budget for Kimi Coding kimi-for-coding-highspeed", upstreamCatalogModel(t, "kimi-coding", "kimi-for-coding-highspeed"), ThinkingMedium, "adaptive", "medium"},
		// .upstream/v0.87.1/packages/ai/test/anthropic-force-adaptive-thinking.test.ts:106
		{"allows built-in adaptive models to opt out with compat.forceAdaptiveThinking false", optOut, ThinkingMedium, "enabled", ""},
		// .upstream/v0.87.1/packages/ai/test/anthropic-force-adaptive-thinking.test.ts:117 (reasoning omitted, not explicitly off)
		{"preserves thinking.type=disabled when reasoning is off regardless of override", customAdaptiveModel(&ModelCompat{ForceAdaptiveThinking: new(true)}), "", "disabled", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := *tc.model
			model.ProviderMeta.BaseURL = "http://127.0.0.1:9"
			payload := captureUpstreamPayloadCall(t, func(options StreamOptions) (*AssistantMessageEventStream, error) {
				return StreamSimple(t.Context(), &model, NormalizeContext(ctx), options)
			}, StreamOptions{APIKey: "fake-key", Thinking: tc.level.ReasoningOption()})
			if tc.thinking == "enabled" {
				var thinking struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(payload["thinking"], &thinking); err != nil {
					t.Fatal(err)
				}
				if thinking.Type != "enabled" {
					t.Fatalf("thinking = %s", payload["thinking"])
				}
			} else if tc.thinking == "disabled" {
				assertShapeJSON(t, payload["thinking"], `{"type":"disabled"}`)
			} else {
				assertShapeJSON(t, payload["thinking"], `{"type":"adaptive","display":"summarized"}`)
			}
			if tc.output == "" {
				requireAbsentJSON(t, payload, "output_config")
			} else {
				assertShapeJSON(t, payload["output_config"], `{"effort":"`+tc.output+`"}`)
			}
		})
	}
}

func TestAnthropicSimpleCallerPreservesCustomModel(t *testing.T) {
	// packages/ai/test/anthropic-force-adaptive-thinking.test.ts:117 also requires the streamSimple caller to retain the custom model's reasoning capability.
	model := customAdaptiveModel(&ModelCompat{ForceAdaptiveThinking: new(true)})
	captured := errors.New("payload captured")
	var payload map[string]json.RawMessage
	stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{APIKey: "fake-key", Thinking: "", OnPayload: func(value any, _ *Model) (any, error) {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &payload); err != nil {
			return nil, err
		}
		return nil, captured
	}})
	result := requireAnthropicSetupError(t, stream, err)
	if result.ErrorMessage != captured.Error() {
		t.Fatalf("capture error=%q", result.ErrorMessage)
	}
	assertShapeJSON(t, payload["thinking"], `{"type":"disabled"}`)
	requireAbsentJSON(t, payload, "output_config")
}

func TestAnthropicUpstreamTemperatureCompat(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		temp     float64
		present  bool
	}{
		// .upstream/v0.87.1/packages/ai/test/anthropic-temperature-compat.test.ts:68
		{"omits temperature for Claude Opus 4.7", "claude-opus-4-7", 0, false},
		// .upstream/v0.87.1/packages/ai/test/anthropic-temperature-compat.test.ts:74
		{"omits temperature for Claude Opus 4.8", "claude-opus-4-8", 0, false},
		// .upstream/v0.87.1/packages/ai/test/anthropic-temperature-compat.test.ts:80
		{"omits default temperature for Claude Opus 4.7", "claude-opus-4-7", 1, false},
		// .upstream/v0.87.1/packages/ai/test/anthropic-temperature-compat.test.ts:86
		{"keeps temperature for Claude Opus 4.6", "claude-opus-4-6", 0, true},
		// .upstream/v0.87.1/packages/ai/test/anthropic-temperature-compat.test.ts:92
		{"keeps temperature for Claude Sonnet 4.6", "claude-sonnet-4-6", 0, true},
		// .upstream/v0.87.1/packages/ai/test/anthropic-temperature-compat.test.ts:98
		{"omits temperature for custom models with supportsTemperature disabled", "vendor--claude-opus-4-7", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := AnthropicConfig{Model: tc.id, APIKey: "fake-key", BaseURL: "http://127.0.0.1:9"}
			if strings.HasPrefix(tc.id, "vendor") {
				config.ProviderID = "vendor-proxy"
				config.Compat = &AnthropicMessagesCompat{SupportsTemperature: new(false)}
			}
			payload := captureAnthropicUpstreamPayload(t, NewAnthropicProvider(config), Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}, StreamOptions{Temperature: tc.temp, TemperatureSet: true, Thinking: ""})
			if tc.present {
				assertShapeJSON(t, payload["temperature"], "0")
			} else {
				requireAbsentJSON(t, payload, "temperature")
			}
		})
	}
}
