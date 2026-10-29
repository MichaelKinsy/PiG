package providers

// Ports packages/ai/catalog/providers/meta.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func meta() ProviderDefinition {
	return ProviderDefinition{
		ID: "meta",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "meta", ModelsDevOptions{API: "openai-responses", BaseURL: "https://api.meta.ai/v1"})
		},
	}
}
