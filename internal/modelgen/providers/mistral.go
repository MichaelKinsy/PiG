package providers

// Ports packages/ai/catalog/providers/mistral.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func mistral() ProviderDefinition {
	return ProviderDefinition{
		ID: "mistral",
		// No SkipDeprecated: models.dev marks Mistral models deprecated as soon as Mistral announces their
		// retirement, while they keep working, e.g. devstral-2512: marked 2026-06-02, retired 2026-07-31.
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "mistral", ModelsDevOptions{
				API:               "mistral-conversations",
				BaseURL:           "https://api.mistral.ai",
				NoRecordReasoning: true,
				Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
					// Models with effort values use `reasoning_effort` with these levels.
					// Reasoning models without them (Magistral) use `prompt_mode`.
					thinkingLevelMap := GetEffortThinkingLevelMap(source.ReasoningOptions)
					result := Spread(model)
					if thinkingLevelMap != nil {
						result.Set("thinkingLevelMap", thinkingLevelMap)
					}
					// Mistral bills cached input at 10% when models.dev does not say otherwise.
					var cacheRead float64
					if source.Cost.CacheRead != nil {
						cacheRead = *source.Cost.CacheRead
					} else if googleOr(source.Cost.Input, 0) != 0 {
						cacheRead = RoundCost(*source.Cost.Input * 0.1)
					}
					result.Set("cost", O(
						"input", googleOr(source.Cost.Input, 0),
						"output", googleOr(source.Cost.Output, 0),
						"cacheRead", cacheRead,
						"cacheWrite", googleOr(source.Cost.CacheWrite, 0),
					))
					return result, nil
				},
			})
		},
	}
}
