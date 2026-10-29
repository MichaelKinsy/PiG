package providers

// Ports packages/ai/catalog/providers/qwen-token-plan.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// Alibaba Cloud Model Studio Token Plan. International and China use separate
// endpoints and API keys (sk-sp- prefix). models.dev lists them as
// "alibaba-token-plan[-cn]".
const interationalBaseURL = "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"

func qwenTokenPlanCompat() *Obj {
	return O(
		"thinkingFormat", "qwen",
		"supportsDeveloperRole", false,
		"supportsStore", false,
		"supportsReasoningEffort", true,
	)
}

func qwenTokenPlanFallbackThinkingLevels() *Obj {
	return O(
		"minimal", nil,
		"low", nil,
		"medium", nil,
		"high", "high",
		"xhigh", nil,
		"max", "max",
	)
}

var qwenTokenPlanReasoningEffortFallbackModelIDs = map[string]bool{"glm-5": true, "glm-5.1": true}

// Retired preview id — models.dev may still list it after GA ships.
var qwenTokenPlanExcludedModelIDs = map[string]bool{"qwen3.8-max-preview": true}

// qwenTokenPlanModels maps one models.dev token plan provider. A nil modelIds keeps every model.
func qwenTokenPlanModels(ctx *ProviderContext, source, baseURL string, modelIds map[string]bool) ([]ChatModel, error) {
	return ModelsDevModels(ctx, source, ModelsDevOptions{
		API:               "openai-completions",
		BaseURL:           baseURL,
		NoRecordReasoning: true,
		Include: func(model *ModelsDevModel) bool {
			return !qwenTokenPlanExcludedModelIDs[model.ID] && (modelIds == nil || modelIds[model.ID])
		},
		Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
			thinkingLevelMap := GetEffortThinkingLevelMap(source.ReasoningOptions)
			if thinkingLevelMap == nil && qwenTokenPlanReasoningEffortFallbackModelIDs[model.Str("id")] {
				thinkingLevelMap = qwenTokenPlanFallbackThinkingLevels()
			}
			var compat *Obj
			if thinkingLevelMap != nil {
				compat = qwenTokenPlanCompat()
			} else {
				compat = Spread(qwenTokenPlanCompat(), O("supportsReasoningEffort", false))
			}
			out := Spread(model, O("compat", compat))
			if thinkingLevelMap != nil {
				out.Set("thinkingLevelMap", thinkingLevelMap)
			}
			return out, nil
		},
	})
}

func qwenCompletionsCompat() *Obj { return O("supportsStrictMode", true) }

func qwenTokenPlan() ProviderDefinition {
	return ProviderDefinition{
		ID: "qwen-token-plan",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return qwenTokenPlanModels(ctx, "alibaba-token-plan", interationalBaseURL, nil)
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(qwenCompletionsCompat())},
	}
}
