package providers

// Ports packages/ai/catalog/providers/zai.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// Tool-call streaming (`tool_stream`) is rejected by the GLM-4.5 generation.
var zaiToolStreamUnsupportedModelIDs = map[string]bool{"glm-4.5": true, "glm-4.5-air": true, "glm-4.5-flash": true, "glm-4.5v": true}

// The Z.AI coding plans. The international and China plans list the same models.
func zaiCodingPlanModels(ctx *ProviderContext, source, baseURL string) ([]ChatModel, error) {
	// Coding-plan entries carry plan prices; show the pay-as-you-go API rates.
	apiModels := map[string]*ModelsDevModel{}
	for _, model := range ModelsDevChatModels(ctx.Sources.ModelsDev, "zai") {
		apiModels[model.ID] = model
	}
	return ModelsDevModels(ctx, source, ModelsDevOptions{
		API:     "openai-completions",
		BaseURL: baseURL,
		Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
			id := model.Str("id")
			thinkingLevelMap := GetEffortThinkingLevelMap(source.ReasoningOptions)
			if thinkingLevelMap != nil && (id == "glm-5.2" || id == "glm-5.2-highspeed") {
				thinkingLevelMap.Set("off", "none")
			}
			cost := source.Cost
			if apiModel := apiModels[id]; apiModel != nil && apiModel.Source.Object("cost") != nil {
				cost = apiModel.Cost
			}
			compat := O(
				"supportsDeveloperRole", false,
				"thinkingFormat", "zai",
			)
			if thinkingLevelMap != nil {
				compat.Set("supportsReasoningEffort", true)
			}
			if !zaiToolStreamUnsupportedModelIDs[id] {
				compat.Set("zaiToolStream", true)
			}
			out := Spread(model)
			if thinkingLevelMap != nil {
				out.Set("thinkingLevelMap", thinkingLevelMap)
			}
			out.Set("cost", ModelsDevToCost(cost))
			out.Set("compat", compat)
			return out, nil
		},
	})
}

func zaiCompletionsCompat() *Obj {
	return O(
		"supportsStore", false,
		"supportsDeveloperRole", false,
		"supportsReasoningEffort", false,
		"maxTokensField", "max_tokens",
		"thinkingFormat", "zai",
		"supportsStrictMode", true,
	)
}

func zai() ProviderDefinition {
	return ProviderDefinition{
		ID: "zai",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return zaiCodingPlanModels(ctx, "zai-coding-plan", "https://api.z.ai/api/coding/paas/v4")
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(zaiCompletionsCompat())},
	}
}
