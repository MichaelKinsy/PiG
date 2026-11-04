package providers

// Ports packages/ai/catalog/providers/groq.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func groq() ProviderDefinition {
	return ProviderDefinition{
		ID: "groq",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "groq", ModelsDevOptions{API: "openai-completions", BaseURL: "https://api.groq.com/openai/v1"})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStrictMode", true))},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			if model.Str("id") == "qwen/qwen3.6-27b" {
				MergeThinking(model, O("minimal", nil, "low", nil, "medium", nil, "high", "default"))
			}
		},
	}
}
