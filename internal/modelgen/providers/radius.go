package providers

// Ports packages/ai/catalog/providers/radius.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// The unauthenticated public catalog; authenticated clients overlay it at runtime.
func radius() ProviderDefinition {
	return ProviderDefinition{
		ID: "radius",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			models := make([]ChatModel, 0, len(ctx.Sources.Radius))
			for _, model := range ctx.Sources.Radius {
				models = append(models, model.Clone())
			}
			return models, nil
		},
	}
}
