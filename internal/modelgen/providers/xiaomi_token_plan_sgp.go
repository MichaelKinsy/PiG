package providers

// Ports packages/ai/catalog/providers/xiaomi-token-plan-sgp.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func xiaomiTokenPlanSgp() ProviderDefinition {
	return ProviderDefinition{
		ID: "xiaomi-token-plan-sgp",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return xiaomiModels(ctx, "https://token-plan-sgp.xiaomimimo.com/v1")
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(xiaomiCompletionsCompat())},
	}
}
