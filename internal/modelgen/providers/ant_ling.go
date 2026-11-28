package providers

// Ports packages/ai/catalog/providers/ant-ling.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// Ant Ling (listed as "bailing" on models.dev). Ling models think by default and
// only `thinking: { type: "disabled" }` turns it off; `reasoning_effort` and
// `reasoning.effort` are ignored. models.dev lists thinking as an on/off toggle,
// so expose only off and high.
func antLing() ProviderDefinition {
	return ProviderDefinition{
		ID: "ant-ling",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "bailing", ModelsDevOptions{
				API:               "openai-completions",
				BaseURL:           "https://api.ant-ling.com/v1",
				SkipDeprecated:    true,
				NoRecordReasoning: true,
				Map: func(model ChatModel, _ *ModelsDevModel) (ChatModel, error) {
					if !model.Bool("reasoning") {
						return model, nil
					}
					return Spread(model, O(
						"thinkingLevelMap", O(
							"off", "off",
							"minimal", nil,
							"low", nil,
							"medium", nil,
							"high", "high",
							"xhigh", nil,
							"max", nil,
						),
					)), nil
				},
			})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O(
			"supportsStore", false,
			"supportsReasoningEffort", false,
			"maxTokensField", "max_tokens",
			"thinkingFormat", "deepseek",
			"supportsStrictMode", true,
			"supportsLongCacheRetention", false,
		))},
	}
}
