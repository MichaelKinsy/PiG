package providers

// Ports packages/ai/catalog/providers/cerebras.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func cerebras() ProviderDefinition {
	return ProviderDefinition{
		ID: "cerebras",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "cerebras", ModelsDevOptions{API: "openai-completions", BaseURL: "https://api.cerebras.ai/v1"})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStore", false, "supportsDeveloperRole", false))},
	}
}
