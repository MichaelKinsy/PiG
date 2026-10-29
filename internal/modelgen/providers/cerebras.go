package providers

// Ports packages/ai/catalog/providers/cerebras.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func cerebras() ProviderDefinition {
	return ProviderDefinition{
		ID: "cerebras",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "cerebras", ModelsDevOptions{API: "openai-completions", BaseURL: "https://api.cerebras.ai/v1"})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStore", false, "supportsDeveloperRole", false))},
	}
}
