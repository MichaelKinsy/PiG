package providers

// Ports packages/ai/catalog/providers/minimax-cn.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func minimaxCn() ProviderDefinition {
	return ProviderDefinition{
		ID: "minimax-cn",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return minimaxModels(ctx, "minimax-cn", "https://api.minimaxi.com/anthropic")
		},
	}
}
