package providers

// Ports packages/ai/catalog/providers/meta.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func meta() ProviderDefinition {
	return ProviderDefinition{
		ID: "meta",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "meta", ModelsDevOptions{API: "openai-responses", BaseURL: "https://api.meta.ai/v1"})
		},
	}
}
