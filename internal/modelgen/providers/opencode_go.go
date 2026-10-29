package providers

// Ports packages/ai/catalog/providers/opencode-go.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

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
