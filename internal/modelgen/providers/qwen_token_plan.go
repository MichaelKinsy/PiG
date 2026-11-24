package providers

// Ports packages/ai/catalog/providers/qwen-token-plan.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// Qwen Token Plan: QwenCloud (international) and Qianwen AI Platform (China). Each region has its
// own endpoint and API keys (sk-sp- prefix). models.dev lists them as "alibaba-token-plan[-cn]".
// The former token-plan.{ap-southeast-1,cn-beijing}.maas.aliyuncs.com hosts resolve to the same
// gateways and keep working for configurations that still name them.
// https://docs.qwencloud.com/developer-guides/getting-started/regions
// https://platform.qianwenai.com/docs/developer-guides/clients-and-developer-tools/other-tools
const interationalBaseURL = "https://token-plan.maas.qwencloudapi.com/compatible-mode/v1"
const cnBaseURL = "https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1"

func qwenTokenPlanCompat() *Obj {
	return O(
		"thinkingFormat", "qwen",
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

// qwenTokenPlanModels maps one models.dev token plan provider. A nil modelIds keeps every model.
func qwenTokenPlanModels(ctx *ProviderContext, source, baseURL string, modelIds map[string]bool) ([]ChatModel, error) {
	return ModelsDevModels(ctx, source, ModelsDevOptions{
		API:               "openai-completions",
		BaseURL:           baseURL,
		NoRecordReasoning: true,
		// models.dev marks models dropped from the Token Plan lists deprecated, e.g. qwen3.8-max-preview,
		// deepseek-v3.2 and deepseek-v4-flash.
		SkipDeprecated: true,
		Include: func(model *ModelsDevModel) bool {
			return modelIds == nil || modelIds[model.ID]
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
