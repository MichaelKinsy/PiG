package providers

// Ports packages/ai/catalog/providers/fireworks.ts

import (
	"strings"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

// fireworksAnthropicCompat is the Fireworks Anthropic-compatible API. Prompt caching uses automatic prefix
// matching and session affinity: x-session-affinity routes requests to the same
// replica for cache hits. cache_control on tools and eager_input_streaming are
// not supported.
// https://docs.fireworks.ai/tools-sdks/anthropic-compatibility
func fireworksAnthropicCompat() *Obj {
	return O(
		"allowEmptySignature", true,
		"sendSessionAffinityHeaders", true,
		"supportsEagerToolInputStreaming", false,
		"supportsCacheControlOnTools", false,
		"supportsLongCacheRetention", false,
	)
}

func fireworksOpenAICompat() *Obj {
	return O(
		"supportsStore", false,
		"supportsDeveloperRole", false,
		"sendSessionAffinityHeaders", true,
		"supportsLongCacheRetention", false,
	)
}

func fireworksKimiK3Compat() *Obj {
	return Spread(fireworksOpenAICompat(), O(
		"requiresReasoningContentOnAssistantMessages", true,
		"thinkingFormat", "openai",
	))
}

// Verified against Fireworks Messages raw_output on 2026-09-10 (#9323).
// Fall back to verified support when models.dev omits effort metadata; this is
// not an allowlist. Any Fireworks Messages model advertising effort uses adaptive thinking.
var fireworksAdaptiveThinkingFallbackModels = map[string]bool{
	"accounts/fireworks/models/deepseek-v4-flash-0731":       true,
	"accounts/fireworks/models/deepseek-v4-flash-vision-exp": true,
	"accounts/fireworks/models/deepseek-v4-pro-0813":         true,
	"accounts/fireworks/models/qwen3p8-max":                  true,
	"accounts/fireworks/models/qwen3p8-2p4t-a95b":            true,
}

func fireworks() ProviderDefinition {
	return ProviderDefinition{
		ID: "fireworks",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "fireworks-ai", ModelsDevOptions{
				API: "anthropic-messages",
				// SDK appends /v1/messages.
				BaseURL: "https://api.fireworks.ai/inference",
				Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
					id := model.Str("id")
					if strings.Contains(id, "glm-") || strings.Contains(id, "kimi-k3") {
						compat := fireworksOpenAICompat()
						if strings.Contains(id, "kimi-k3") {
							compat = fireworksKimiK3Compat()
						}
						return Spread(model, O(
							"api", "openai-completions",
							"baseUrl", "https://api.fireworks.ai/inference/v1",
							"compat", compat,
						)), nil
					}
					// Use adaptive thinking for cataloged effort controls, with verified
					// fallbacks where models.dev is incomplete. New models need no allowlist entry.
					adaptive := source.HasEffortOption() || fireworksAdaptiveThinkingFallbackModels[id]
					compat := fireworksAnthropicCompat()
					if adaptive {
						compat.Set("forceAdaptiveThinking", true)
					}
					return Spread(model, O("compat", compat)), nil
				},
			})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStrictMode", true))},
		Finalize: func(model ChatModel, ctx *ProviderContext) {
			id := model.Str("id")
			if model.Str("api") == "anthropic-messages" && model.Object("compat").Bool("forceAdaptiveThinking") {
				// Qwen Max currently advertises only a toggle. Prefer upstream effort
				// metadata once available instead of replacing it with this fallback.
				if id == "accounts/fireworks/models/qwen3p8-max" && !model.Has("thinkingLevelMap") {
					low, medium, xhigh := "low", "medium", "xhigh"
					if levels := GetEffortThinkingLevelMap([]ModelsDevReasoningOption{{Type: "effort", Values: []*string{&low, &medium, &xhigh}}}); levels != nil {
						model.Set("thinkingLevelMap", levels)
					}
				}
				source := ctx.ModelsDevSource(model)
				if (source != nil && source.HasToggleOption()) ||
					// The 2.4T alias omits the verified toggle in models.dev.
					id == "accounts/fireworks/models/qwen3p8-2p4t-a95b" {
					MergeThinking(model, O("off", "none"))
				}
				if id == "accounts/fireworks/models/deepseek-v4-pro-0813" {
					MergeThinking(model, O("low", "low"))
				}
			}
			if strings.Contains(id, "glm-5p2") {
				// GLM 5.2 and its fast router support off/high/max. Fireworks maps low
				// and medium to high, so do not expose those aliases as distinct levels.
				MergeThinking(model, O("off", "none", "minimal", nil, "low", nil, "medium", nil, "max", "max"))
			}
			if strings.Contains(id, "kimi-k3") {
				// Fireworks maps medium to high on both APIs; do not expose it as a distinct level.
				MergeThinking(model, O("medium", nil))
				// Kimi K3 accepts tool-bearing system messages; Fireworks passes them through.
				if model.Str("api") == "openai-completions" {
					MergeCompat(model, O("supportsMidConvoSystemMessages", true, "supportsMidConvoToolAdditions", true))
				}
			}
		},
	}
}
