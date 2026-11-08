package providers

// Ports packages/ai/catalog/providers/qwen-token-plan-individual.ts

import (
	. "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts
	"github.com/MichaelKinsy/PiG/internal/modelgen/modeldata"
)

// QwenCloud Token Plan Individual text-model allowlist, verified 2026-11-01.
// It shares the international endpoint with a narrower catalog; qwen-token-plan-individual-cn uses it on the China endpoint.
// https://docs.qwencloud.com/token-plan/personal/token-plan-personal-overview
var qwenTokenPlanIndividualModelIDs = []string{
	"deepseek-v4-flash-0731",
	"deepseek-v4-pro",
	"deepseek-v4-pro-0813",
	"glm-5.2",
	"glm-5.3",
	"qwen3.6-flash",
	"qwen3.7-max",
	"qwen3.7-plus",
	"qwen3.8-flash",
	"qwen3.8-max",
}

// qwenTokenPlanIndividualModels serves the Individual allowlist from one models.dev token plan catalog.
func qwenTokenPlanIndividualModels(ctx *ProviderContext, source, baseURL string) ([]ChatModel, error) {
	allowed := map[string]bool{}
	for _, id := range qwenTokenPlanIndividualModelIDs {
		allowed[id] = true
	}
	models, err := qwenTokenPlanModels(ctx, source, baseURL, allowed)
	if err != nil {
		return nil, err
	}
	if ctx.Strict {
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.Str("id"))
		}
		if err := modeldata.AssertExactModelIds(ctx.ProviderID, qwenTokenPlanIndividualModelIDs, ids); err != nil {
			return nil, err
		}
	}
	return models, nil
}

func qwenTokenPlanIndividual() ProviderDefinition {
	return ProviderDefinition{
		ID: "qwen-token-plan-individual",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return qwenTokenPlanIndividualModels(ctx, "alibaba-token-plan", interationalBaseURL)
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(qwenCompletionsCompat())},
	}
}
