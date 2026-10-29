package providers

// Ports packages/ai/catalog/providers/xiaomi-token-plan-ams.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func xiaomiTokenPlanAms() ProviderDefinition {
	return ProviderDefinition{
		ID: "xiaomi-token-plan-ams",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return xiaomiModels(ctx, "https://token-plan-ams.xiaomimimo.com/v1")
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(xiaomiCompletionsCompat())},
	}
}
