package providers

// Ports packages/ai/catalog/providers/azure.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// Azure Foundry deploys these with larger context windows than OpenAI's own short-tier defaults.
// See models-sold-directly-by-azure docs.
var azureContextWindowOverrides = map[string]float64{
	"gpt-5.4":       1050000,
	"gpt-5.5":       1050000,
	"gpt-5.6-luna":  1050000,
	"gpt-5.6-sol":   1050000,
	"gpt-5.6-terra": 1050000,
}

// Azure resells DeepSeek at its own rates. US data zone, checked 2026-09-16.
// https://azure.microsoft.com/en-us/pricing/details/ai-foundry-models/deepseek/
func azureDeepseekV4ProCost() *Obj {
	return O("input", 1.925, "output", 3.828, "cacheRead", 0.165, "cacheWrite", 0)
}

// azureOpenAIModels are OpenAI models as Azure deploys them. Azure bills without OpenAI's long-context tier.
func azureOpenAIModels(models []ChatModel) []ChatModel {
	out := []ChatModel{}
	for _, model := range models {
		if model.Str("api") != "openai-responses" {
			continue
		}
		cost := model.Object("cost")
		window, ok := azureContextWindowOverrides[model.Str("id")]
		if !ok {
			window = model.Num("contextWindow")
		}
		out = append(out, Spread(model).
			Set("api", "azure-openai-responses").
			Set("provider", "azure").
			Set("baseUrl", "").
			Set("cost", O(
				"input", cost.Num("input"),
				"output", cost.Num("output"),
				"cacheRead", cost.Num("cacheRead"),
				"cacheWrite", cost.Num("cacheWrite"),
			)).
			Set("contextWindow", window))
	}
	return out
}

func azure() ProviderDefinition {
	return ProviderDefinition{
		ID: "azure",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			openaiModels, err := ctx.ProviderModels("openai")
			if err != nil {
				return nil, err
			}
			deepseekModels, err := ctx.ProviderModels("deepseek")
			if err != nil {
				return nil, err
			}
			models := azureOpenAIModels(openaiModels)
			for _, model := range deepseekModels {
				if model.Str("id") != "deepseek-v4-pro" {
					continue
				}
				models = append(models, Spread(model).
					Set("provider", "azure").
					Set("baseUrl", "").
					Set("cost", azureDeepseekV4ProCost()))
			}
			return models, nil
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStrictMode", true))},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			EnableOpenAIGrammarTools(model)
			if model.Str("api") == "openai-completions" {
				// Azure 400s on DeepSeek's `thinking` field and on every prompt cache parameter, discards a
				// `developer` system message unbilled once reasoning_effort is set, and honours mid-convo ones (#9645).
				MergeCompat(model, O(
					"supportsDeveloperRole", false,
					"supportsMidConvoSystemMessages", true,
					"thinkingFormat", "openai",
					"supportsLongCacheRetention", false,
				))
			}
		},
	}
}
