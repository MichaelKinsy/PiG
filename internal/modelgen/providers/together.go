package providers

// Ports packages/ai/catalog/providers/together.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func togetherBaseCompat() *Obj {
	return O(
		"supportsStore", false,
		"supportsReasoningEffort", false,
		"maxTokensField", "max_tokens",
		"supportsStrictMode", false,
		"supportsLongCacheRetention", false,
	)
}

func togetherToggleReasoningCompat() *Obj {
	return Spread(togetherBaseCompat(), O("thinkingFormat", "together"))
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
					// Every reasoning model Together lists takes its thinking toggle. (The effort-controlled ones, gpt-oss and
					// DeepSeek V4 Pro 0813, are deprecated upstream and skipped.)
					return Spread(model, O(
						"compat", togetherToggleReasoningCompat(),
						"thinkingLevelMap", O("minimal", nil, "low", nil, "medium", nil),
					)), nil
				},
			})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O(
			"supportsStore", false,
			"supportsReasoningEffort", false,
			"maxTokensField", "max_tokens",
			"thinkingFormat", "together",
			"supportsLongCacheRetention", false,
		))},
	}
}
