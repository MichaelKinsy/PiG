package providers

// Ports packages/ai/catalog/providers/qwen-token-plan-cn.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func qwenTokenPlanCn() ProviderDefinition {
	return ProviderDefinition{
		ID: "qwen-token-plan-cn",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return qwenTokenPlanModels(ctx, "alibaba-token-plan-cn", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", nil)
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(qwenCompletionsCompat())},
	}
}
