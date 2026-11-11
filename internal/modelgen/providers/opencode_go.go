package providers

// Ports packages/ai/catalog/providers/opencode-go.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func opencodeGo() ProviderDefinition {
	return ProviderDefinition{
		ID:     "opencode-go",
		Models: opencodeModels,
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(opencodeCompletionsCompat())},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			finalizeOpencodeModel(model)
		},
	}
}
