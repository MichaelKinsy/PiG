package providers

// Ports packages/ai/catalog/providers/qwen-token-plan-cn.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func qwenTokenPlanCn() ProviderDefinition {
	return ProviderDefinition{
		ID: "qwen-token-plan-cn",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return qwenTokenPlanModels(ctx, "alibaba-token-plan-cn", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", nil)
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(qwenCompletionsCompat())},
	}
}
