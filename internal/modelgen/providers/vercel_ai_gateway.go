package providers

// Ports packages/ai/catalog/providers/vercel-ai-gateway.ts

import (
	"strings"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

func vercelAiGateway() ProviderDefinition {
	return ProviderDefinition{
		ID: "vercel-ai-gateway",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			models := CloneAll(ctx.Sources.AIGateway.Chat)
			for i, model := range models {
				// Keep Kimi K3's canonical output limit when gateway metadata is missing or incorrect.
				id := model.Str("id")
				if strings.HasPrefix(id, "moonshotai/") && IsKimiK3FamilyID(strings.TrimPrefix(id, "moonshotai/")) {
					models[i] = Spread(model, O("maxTokens", KimiK3MaxTokens))
				}
			}
			return models, nil
		},
		Classifiers: func(ctx *ProviderContext) ([]*Obj, error) {
			return CloneAll(ctx.Sources.AIGateway.Classifiers), nil
		},
	}
}
