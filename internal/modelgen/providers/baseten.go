package providers

// Ports packages/ai/catalog/providers/baseten.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func basetenBaseCompat() *Obj {
	return O(
		"supportsStore", false,
		"supportsDeveloperRole", false,
		"supportsReasoningEffort", false,
		"supportsUsageInStreaming", true,
		"maxTokensField", "max_tokens",
		"supportsStrictMode", true,
		// Baseten automatic prompt caching needs session affinity so related
		// requests land on the same replica. See:
		// https://docs.baseten.co/inference/model-apis/pricing-and-limits
		"sendSessionAffinityHeaders", true,
		"supportsLongCacheRetention", false,
	)
}

func basetenReasoningEffortCompat() *Obj {
	return Spread(basetenBaseCompat(), O("supportsReasoningEffort", true, "thinkingFormat", "openai"))
}

func basetenToggleReasoningCompat() *Obj {
	return Spread(basetenBaseCompat(), O(
		"thinkingFormat", "baseten",
		"chatTemplateArgs", O("enable_thinking", O("$var", "thinking.enabled")),
	))
}

func basetenToggleReasoningEffortCompat() *Obj {
	return Spread(basetenReasoningEffortCompat(), O(
		"thinkingFormat", "baseten",
		"chatTemplateArgs", O("enable_thinking", O("$var", "thinking.enabled")),
	))
}

func basetenToggleLevels() *Obj {
	return O("off", "off", "minimal", nil, "low", nil, "medium", nil, "high", "high", "xhigh", nil, "max", nil)
}

func basetenGlm52Levels() *Obj {
	return O("off", "none", "minimal", nil, "low", nil, "medium", nil, "high", "high", "xhigh", nil, "max", "max")
}

func basetenGlm52ModelIDs(id string) bool {
	return id == "zai-org/GLM-5.2" || id == "zai-org/GLM-5.2-Fast"
}

func baseten() ProviderDefinition {
	return ProviderDefinition{
		ID: "baseten",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "baseten", ModelsDevOptions{
				API:               "openai-completions",
				BaseURL:           "https://inference.baseten.co/v1",
				SkipDeprecated:    true,
				NoRecordReasoning: true,
				Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
					options := source.ReasoningOptions
					isGlm52 := basetenGlm52ModelIDs(model.Str("id"))
					supportsToggle := isGlm52 || source.HasToggleOption()
					supportsEffort := isGlm52 || source.HasEffortOption()
					var compat *Obj
					switch {
					case supportsToggle && supportsEffort:
						compat = basetenToggleReasoningEffortCompat()
					case supportsToggle:
						compat = basetenToggleReasoningCompat()
					case supportsEffort:
						compat = basetenReasoningEffortCompat()
					default:
						compat = basetenBaseCompat()
					}
					var thinkingLevelMap *Obj
					switch {
					case isGlm52:
						thinkingLevelMap = basetenGlm52Levels()
					case supportsToggle:
						thinkingLevelMap = basetenToggleLevels()
					default:
						thinkingLevelMap = GetEffortThinkingLevelMap(options)
					}
					result := Spread(model)
					if thinkingLevelMap != nil {
						result.Set("thinkingLevelMap", thinkingLevelMap)
					}
					// Baseten's GLM-5.2 endpoints are text-only despite models.dev reporting image input.
					if !isGlm52 && source.ImageInput {
						result.Set("input", Strings("text", "image"))
					} else {
						result.Set("input", Strings("text"))
					}
					result.Set("compat", compat)
					return result, nil
				},
			})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStrictMode", true))},
	}
}
