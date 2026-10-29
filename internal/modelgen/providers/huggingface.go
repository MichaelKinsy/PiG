package providers

// Ports packages/ai/catalog/providers/huggingface.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func huggingface() ProviderDefinition {
	return ProviderDefinition{
		ID: "huggingface",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "huggingface", ModelsDevOptions{
				API:     "openai-completions",
				BaseURL: "https://router.huggingface.co/v1",
				Compat:  O("supportsDeveloperRole", false),
			})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStrictMode", true))},
	}
}
