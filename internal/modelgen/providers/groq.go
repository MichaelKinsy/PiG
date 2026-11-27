package providers

// Ports packages/ai/catalog/providers/groq.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func groq() ProviderDefinition {
	return ProviderDefinition{
		ID: "groq",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "groq", ModelsDevOptions{
				API:     "openai-completions",
				BaseURL: "https://api.groq.com/openai/v1",
				// models.dev has marked Groq models deprecated only after Groq shut them down, then deletes
				// them weeks later, e.g. llama-3.3-70b-versatile: shut down 2026-08-16, marked 2026-10-21.
				SkipDeprecated: true,
			})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStrictMode", true))},
	}
}
