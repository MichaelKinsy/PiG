package providers

// Ports packages/ai/catalog/providers/together.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func togetherBaseCompat() *Obj {
	return O(
		"supportsStore", false,
		"supportsDeveloperRole", false,
		"supportsReasoningEffort", false,
		"maxTokensField", "max_tokens",
		"supportsStrictMode", false,
		"supportsLongCacheRetention", false,
	)
}

func togetherToggleReasoningCompat() *Obj {
	return Spread(togetherBaseCompat(), O("thinkingFormat", "together"))
}

func togetherReasoningEffortCompat() *Obj {
	return Spread(togetherBaseCompat(), O("supportsReasoningEffort", true, "thinkingFormat", "openai"))
}

func togetherToggleReasoningEffortCompat() *Obj {
	return Spread(togetherToggleReasoningCompat(), O("supportsReasoningEffort", true))
}

func together() ProviderDefinition {
	return ProviderDefinition{
		ID: "together",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "togetherai", ModelsDevOptions{
				API:            "openai-completions",
				BaseURL:        "https://api.together.ai/v1",
				SkipDeprecated: true,
				Map: func(model ChatModel, _ *ModelsDevModel) (ChatModel, error) {
					if !model.Bool("reasoning") {
						return Spread(model, O("compat", togetherBaseCompat())), nil
					}
					id := model.Str("id")
					if id == "openai/gpt-oss-20b" || id == "openai/gpt-oss-120b" {
						return Spread(model, O(
							"compat", togetherReasoningEffortCompat(),
							"thinkingLevelMap", O("off", nil, "minimal", nil),
						)), nil
					}
					if id == "deepseek-ai/DeepSeek-V4-Pro-0813" {
						return Spread(model, O(
							"compat", togetherToggleReasoningEffortCompat(),
							"thinkingLevelMap", O("minimal", nil, "low", nil, "medium", nil, "high", "high", "xhigh", nil),
						)), nil
					}
					return Spread(model, O(
						"compat", togetherToggleReasoningCompat(),
						"thinkingLevelMap", O("minimal", nil, "low", nil, "medium", nil),
					)), nil
				},
			})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O(
			"supportsStore", false,
			"supportsDeveloperRole", false,
			"supportsReasoningEffort", false,
			"maxTokensField", "max_tokens",
			"thinkingFormat", "together",
			"supportsLongCacheRetention", false,
		))},
	}
}
