package providers

// Ports packages/ai/catalog/providers/xiaomi-token-plan-ams.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func xiaomiTokenPlanAms() ProviderDefinition {
	return ProviderDefinition{
		ID: "xiaomi-token-plan-ams",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return xiaomiModels(ctx, "https://token-plan-ams.xiaomimimo.com/v1")
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(xiaomiCompletionsCompat())},
	}
}
