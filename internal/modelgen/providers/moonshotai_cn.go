package providers

// Ports packages/ai/catalog/providers/moonshotai-cn.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func moonshotaiCn() ProviderDefinition {
	return ProviderDefinition{
		ID: "moonshotai-cn",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return moonshotModels(ctx, "moonshotai-cn", "https://api.moonshot.cn/v1")
		},
		Compat:   ApiCompatDefaults{"openai-completions": FixedCompat(moonshotCompletionsCompat())},
		Finalize: finalizeMoonshotModel,
	}
}
