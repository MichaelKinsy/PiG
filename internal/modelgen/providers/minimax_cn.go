package providers

// Ports packages/ai/catalog/providers/minimax-cn.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func minimaxCn() ProviderDefinition {
	return ProviderDefinition{
		ID: "minimax-cn",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return minimaxModels(ctx, "minimax-cn", "https://api.minimaxi.com/anthropic")
		},
	}
}
