package ai

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/modelgen"
)

func TestOpenRouterReasoningPayloadsUpstream(t *testing.T) {
	mandatory := ThinkingLevelMap{}
	for level, value := range modelgen.GetOpenRouterThinkingLevelMap(&modelgen.OpenRouterReasoningMetadata{Mandatory: true, SupportedEfforts: []*string{new("max"), new("high"), new("low")}}) {
		mandatory[ThinkingLevel(level)] = value
	}
	for _, tc := range []struct {
		name    string
		mapping ThinkingLevelMap
		level   ThinkingLevel
		want    any
	}{
		// .upstream/v0.87.1/packages/ai/test/openrouter-reasoning-options.test.ts:95
		{"omits reasoning when a background call does not request it", mandatory, "", nil},
		// .upstream/v0.87.1/packages/ai/test/openrouter-reasoning-options.test.ts:99
		{"still sends an explicitly selected supported effort", mandatory, ThinkingLow, map[string]any{"effort": "low"}},
		// .upstream/v0.87.1/packages/ai/test/openrouter-reasoning-options.test.ts:105
		{"continues to explicitly disable reasoning for optional models", nil, "", map[string]any{"effort": "none"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewOpenAIProvider(OpenAIConfig{APIKey: "test", ProviderID: "openrouter", Model: "stealth/ox-alpha", BaseURL: "https://example.invalid/v1", Compat: &OpenAICompat{ThinkingFormat: "openrouter"}, ThinkingLevelMap: tc.mapping})
			payload := captureSamplingPayload(t, provider, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{Thinking: tc.level, IsReasoning: true})
			if !reflect.DeepEqual(payload["reasoning"], tc.want) {
				t.Fatalf("reasoning=%#v, want %#v", payload["reasoning"], tc.want)
			}
			if tc.want == nil {
				if _, exists := payload["reasoning"]; exists {
					t.Fatal("reasoning must be omitted, not null")
				}
			}
		})
	}
}

func TestConfiguredThinkingMapAndUnsetReasoningAcrossOpenAIPaths(t *testing.T) {
	mapping := ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("vendor-low"), ThinkingMedium: nil, ThinkingHigh: new("vendor-high"), ThinkingXHigh: nil, ThinkingMax: nil}
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		for _, level := range []ThinkingLevel{"", ThinkingLow} {
			t.Run(string(api)+"/"+string(level), func(t *testing.T) {
				var provider Provider
				if api == APIOpenAICompletions {
					provider = NewOpenAIProvider(OpenAIConfig{APIKey: "test", ProviderID: "custom", Model: "mapped", BaseURL: "https://example.invalid", Compat: &OpenAICompat{ThinkingFormat: "openrouter"}, ThinkingLevelMap: mapping})
				} else {
					provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test", ProviderID: "custom", Model: "mapped", BaseURL: "https://example.invalid", IsReasoning: true, ThinkingLevelMap: mapping})
				}
				payload := captureSamplingPayload(t, provider, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{Thinking: level, IsReasoning: true})
				reasoning, present := payload["reasoning"]
				if level == "" {
					if present {
						t.Fatalf("unrequested reasoning=%#v", reasoning)
					}
					return
				}
				if !present || reasoning.(map[string]any)["effort"] != "vendor-low" {
					t.Fatalf("mapped reasoning=%#v", reasoning)
				}
			})
		}
	}
}
