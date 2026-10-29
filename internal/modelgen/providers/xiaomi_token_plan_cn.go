package providers

// Ports packages/ai/catalog/providers/xiaomi-token-plan-cn.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func xiaomiTokenPlanCn() ProviderDefinition {
	return ProviderDefinition{
		ID: "xiaomi-token-plan-cn",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return xiaomiModels(ctx, "https://token-plan-cn.xiaomimimo.com/v1")
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(xiaomiCompletionsCompat())},
	}
}
