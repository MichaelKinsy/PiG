package providers

// Ports packages/ai/catalog/providers/xai.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// Retired or unsuitable for pi's built-in catalog.
var xaiExcludedModelIDs = map[string]bool{
	"grok-3":                       true,
	"grok-3-fast":                  true,
	"grok-4.20-0309-non-reasoning": true,
	"grok-4.20-0309-reasoning":     true,
	"grok-build-0.1":               true,
	"grok-code-fast-1":             true,
}

func xai() ProviderDefinition {
	return ProviderDefinition{
		ID: "xai",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "xai", ModelsDevOptions{
				API:     "openai-responses",
				BaseURL: "https://api.x.ai/v1",
				Compat:  O("supportsLongCacheRetention", false),
				Include: func(source *ModelsDevModel) bool { return !xaiExcludedModelIDs[source.ID] },
			})
		},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			// Models without verified effort options must not send the undocumented
			// "none"/"minimal" efforts.
			if model.Str("api") == "openai-responses" && !model.Has("thinkingLevelMap") {
				MergeThinking(model, O("off", nil, "minimal", nil))
			}
		},
	}
}
