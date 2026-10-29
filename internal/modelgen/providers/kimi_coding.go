package providers

// Ports packages/ai/catalog/providers/kimi-coding.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

// kimiCodingImpliedCost returns the implied cost of a model, or nil. Kimi Coding is subscription-backed, so models.dev
// reports zero cost. Use the equivalent Moonshot API rates to estimate the value of subscription usage.
func kimiCodingImpliedCost(id string) *Obj {
	switch id {
	case "k3", "k3-256k":
		return KimiK3Cost()
	case "k3.1-pro":
		return O("input", 2, "output", 10, "cacheRead", 0.2, "cacheWrite", 0)
	case "k3.1-pro-highspeed":
		return O("input", 4, "output", 20, "cacheRead", 0.2, "cacheWrite", 0)
	case "kimi-for-coding":
		return O("input", 0.95, "output", 4, "cacheRead", 0.19, "cacheWrite", 0)
	case "kimi-for-coding-highspeed":
		return O("input", 1.9, "output", 8, "cacheRead", 0.38, "cacheWrite", 0)
	}
	return nil
}

// kimiCodingRate is `source || implied?.key || 0`.
func kimiCodingRate(source *float64, implied *Obj, key string) float64 {
	if value := googleOr(source, 0); value != 0 {
		return value
	}
	if value := implied.Num(key); value != 0 && value == value {
		return value
	}
	return 0
}

// Kimi Coding uses Chat Completions, the same protocol as the official Kimi Code client.
func kimiCoding() ProviderDefinition {
	return ProviderDefinition{
		ID: "kimi-coding",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "kimi-code-plan-global", ModelsDevOptions{
				API:               "openai-completions",
				BaseURL:           "https://api.kimi.com/coding/v1",
				NoRecordReasoning: true,
				Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
					// K3-style models report effort levels and dynamic tool loading (tools declared in
					// later system messages) in https://api.kimi.com/coding/v1/models; models.dev mirrors
					// the effort levels. kimi-for-coding-highspeed reports neither and rejects
					// tool-bearing system messages with a 400.
					isK3Style := source.HasEffortOption()
					implied := kimiCodingImpliedCost(model.Str("id"))
					// K3-style models send reasoning_effort, so expose the effort values models.dev lists.
					var effortLevels *Obj
					if isK3Style {
						effortLevels = GetEffortThinkingLevelMap(source.ReasoningOptions)
					}
					var baseCompat *Obj
					if isK3Style {
						baseCompat = KimiK3OpenAICompat()
					} else {
						baseCompat = KimiOpenAICompat()
					}
					// All models accept system text after the conversation starts.
					compat := Spread(baseCompat, O("supportsMidConvoSystemMessages", true))
					if isK3Style {
						compat.Set("supportsMidConvoToolAdditions", true)
					}
					result := Spread(model)
					result.Set("compat", compat)
					result.Set("cost", O(
						"input", kimiCodingRate(source.Cost.Input, implied, "input"),
						"output", kimiCodingRate(source.Cost.Output, implied, "output"),
						"cacheRead", kimiCodingRate(source.Cost.CacheRead, implied, "cacheRead"),
						"cacheWrite", kimiCodingRate(source.Cost.CacheWrite, implied, "cacheWrite"),
					))
					// Kimi Code reports every model as always-thinking.
					result.Set("thinkingLevelMap", Spread(effortLevels, O("off", nil)))
					return result, nil
				},
			})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStrictMode", true))},
	}
}
