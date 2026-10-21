package ai

import "testing"

// Pi d2e7d8b7 (#11121): packages/ai/scripts/generate-models.ts adds MiniMax-M3.1-Flash to the direct minimax and minimax-cn
// providers with its models.dev effort levels as thinkingLevelMap and compat.forceAdaptiveThinking, and hides only the older
// M2, M2.1, M2.5 and M2.5-highspeed models (it dropped the allowlist that kept M2.7, M2.7-highspeed and M3 alone). PiG's
// catalog is generated from the pinned Pi's own catalog data (cmd/gen-models), so the generator's stage is tested through
// the catalog it produces and the request the model sends.
func TestMiniMaxDirectProvidersFollowTheUpstreamGeneratorStage(t *testing.T) {
	for _, provider := range []string{"minimax", "minimax-cn"} {
		t.Run(provider, func(t *testing.T) {
			for _, hidden := range []string{"MiniMax-M2", "MiniMax-M2.1", "MiniMax-M2.5", "MiniMax-M2.5-highspeed"} {
				if _, ok := LookupModelExact(provider + "/" + hidden); ok {
					t.Errorf("%s/%s is in the catalog; MiniMax asked to hide it (#2596)", provider, hidden)
				}
			}
			for _, kept := range []string{"MiniMax-M2.7", "MiniMax-M2.7-highspeed", "MiniMax-M3"} {
				if _, ok := LookupModelExact(provider + "/" + kept); !ok {
					t.Errorf("%s/%s is missing from the catalog", provider, kept)
				}
			}
			model := upstreamCatalogModel(t, provider, "MiniMax-M3.1-Flash")
			if model.ProviderMeta.API != APIAnthropicMessages || !model.ProviderMeta.Reasoning {
				t.Fatalf("api=%q reasoning=%v", model.ProviderMeta.API, model.ProviderMeta.Reasoning)
			}
			if compat := model.ProviderMeta.Compat; compat == nil || compat.ForceAdaptiveThinking == nil || !*compat.ForceAdaptiveThinking {
				t.Errorf("compat = %+v, want forceAdaptiveThinking", compat)
			}
			// Thinking cannot be turned off for this model: off and minimal map to null, low through max to their own level.
			for _, level := range []ModelThinkingLevel{ThinkingOff, ThinkingMinimal} {
				if mapped, ok := model.ThinkingLevelMap[level]; !ok || mapped != nil {
					t.Errorf("thinkingLevelMap[%q] = %v, %v, want an explicit null", level, mapped, ok)
				}
			}
			for _, level := range []ModelThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax} {
				if mapped := model.ThinkingLevelMap[level]; mapped == nil || *mapped != string(level) {
					t.Errorf("thinkingLevelMap[%q] = %v, want %q", level, mapped, level)
				}
			}
		})
	}
}

// The effort levels go out as output_config.effort with adaptive thinking on a MiniMax model, through the Anthropic Messages
// request a catalog model sends.
func TestMiniMaxM31FlashSendsEffortAsOutputConfig(t *testing.T) {
	ctx := Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}
	for _, provider := range []string{"minimax", "minimax-cn"} {
		for _, level := range []ModelThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax} {
			t.Run(provider+"/"+string(level), func(t *testing.T) {
				model := *upstreamCatalogModel(t, provider, "MiniMax-M3.1-Flash")
				model.ProviderMeta.BaseURL = "http://127.0.0.1:9"
				payload := captureUpstreamPayloadCall(t, func(options StreamOptions) (*AssistantMessageEventStream, error) {
					return StreamSimple(t.Context(), &model, NormalizeContext(ctx), options)
				}, StreamOptions{APIKey: "fake-key", Thinking: level.ReasoningOption()})
				assertShapeJSON(t, payload["thinking"], `{"type":"adaptive","display":"summarized"}`)
				assertShapeJSON(t, payload["output_config"], `{"effort":"`+string(level)+`"}`)
			})
		}
	}
}
