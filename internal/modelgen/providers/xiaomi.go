package providers

// Ports packages/ai/catalog/providers/xiaomi.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

// Xiaomi MiMo. The `xiaomi` provider targets the API billing endpoint; the
// `xiaomi-token-plan-*` providers cover prepaid Token Plan endpoints per region.
// MiMo Chat Completions only documents thinking.type enabled/disabled and
// rejects some reasoning_effort values (e.g. "minimal"). Xiaomi documents all
// effort levels as identical, so expose only off and high.
func xiaomiModels(ctx *ProviderContext, baseURL string) ([]ChatModel, error) {
	return ModelsDevModels(ctx, ctx.ProviderID, ModelsDevOptions{
		API:               "openai-completions",
		BaseURL:           baseURL,
		SkipDeprecated:    true,
		NoRecordReasoning: true,
		Compat: O(
			"supportsReasoningEffort", false,
			"requiresReasoningContentOnAssistantMessages", true,
			"thinkingFormat", "deepseek",
		),
		Map: func(model ChatModel, _ *ModelsDevModel) (ChatModel, error) {
			if !model.Bool("reasoning") {
				return model, nil
			}
			return Spread(model, O("thinkingLevelMap", O(
				"off", "off",
				"minimal", nil,
				"low", nil,
				"medium", nil,
				"high", "high",
				"xhigh", nil,
				"max", nil,
			))), nil
		},
	})
}

func xiaomiCompletionsCompat() *Obj { return O("supportsStrictMode", true) }

func xiaomi() ProviderDefinition {
	return ProviderDefinition{
		ID: "xiaomi",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return xiaomiModels(ctx, "https://api.xiaomimimo.com/v1")
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(xiaomiCompletionsCompat())},
	}
}
