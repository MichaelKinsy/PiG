package providers

// Ports packages/ai/catalog/providers/mistral.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func mistral() ProviderDefinition {
	return ProviderDefinition{
		ID: "mistral",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			models, err := ModelsDevModels(ctx, "mistral", ModelsDevOptions{
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
			if err != nil {
				return nil, err
			}
			medium := O(
				"id", "mistral-medium-3.5",
				"name", "Mistral Medium 3.5",
				"api", "mistral-conversations",
				"provider", "mistral",
				"baseUrl", "https://api.mistral.ai",
				"reasoning", true,
			)
			none, high := "none", "high"
			if levels := GetEffortThinkingLevelMap([]ModelsDevReasoningOption{{Type: "effort", Values: []*string{&none, &high}}}); levels != nil {
				medium.Set("thinkingLevelMap", levels)
			}
			medium.Set("input", Strings("text", "image"))
			medium.Set("cost", O("input", 1.5, "output", 7.5, "cacheRead", 0, "cacheWrite", 0))
			medium.Set("contextWindow", 262144)
			medium.Set("maxTokens", 262144)
			return AddMissing(models, []ChatModel{medium}), nil
		},
	}
}
