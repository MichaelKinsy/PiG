package providers

// Ports packages/ai/catalog/providers/anthropic.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

// anthropicAllowedFallbackModels are the models the API may fall back to, with the price the caller is billed.
var anthropicAllowedFallbackModels = []struct {
	id        string
	fallbacks []string
}{
	{"claude-fable-5", []string{"claude-opus-4-8", "claude-opus-5"}},
	{"claude-opus-5", []string{"claude-opus-4-8"}},
}

var anthropicModelsWith1MContext = map[string]bool{"claude-opus-4-6": true, "claude-sonnet-4-6": true}
var anthropicModels55 = map[string]bool{"claude-opus-5-5": true, "claude-sonnet-5-5": true, "claude-haiku-5-5": true}

func anthropic() ProviderDefinition {
	return ProviderDefinition{
		ID: "anthropic",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "anthropic", ModelsDevOptions{
				API:     "anthropic-messages",
				BaseURL: "https://api.anthropic.com",
				Map: func(model ChatModel, _ *ModelsDevModel) (ChatModel, error) {
					id := model.Str("id")
					if anthropicModels55[id] {
						MergeThinking(model, Claude55ThinkingLevels(id))
					}
					if anthropicModelsWith1MContext[id] {
						model.Set("contextWindow", 1000000)
					}
					return model, nil
				},
			})
		},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			if model.Str("api") != "anthropic-messages" {
				return
			}
			id := model.Str("id")
			if SupportsClaudeMidConvoEffort(id) {
				EnableClaudeMidConvoEffort(model)
			}
			if SupportsClaudeMidConvoSystemMessages(id) {
				MergeCompat(model, O("supportsMidConvoSystemMessages", true, "supportsMidConvoToolChanges", true))
			}
			MergeCompat(model, O("supportsStrictTools", true))
			// Ephemeral entries have a hard five-minute lifetime; `ttl: "1h"` extends
			// it to one hour. Only direct Anthropic is annotated so cache warming does
			// not assume equivalent behavior through proxies.
			// https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching
			model.Set("promptCache", O("short", 300, "long", 3600))
			maxPerRequest := 600
			if model.Num("contextWindow") == 200000 {
				maxPerRequest = 100
			}
			WithInputLimits(model, O(
				"maxRequestBytes", 32*1024*1024,
				"images", O("maxPerRequest", maxPerRequest),
			))
		},
		FinalizeAll: func(models []ChatModel, _ *ProviderContext) {
			modelsByID := map[string]ChatModel{}
			for _, model := range models {
				modelsByID[model.Str("id")] = model
			}
			for _, entry := range anthropicAllowedFallbackModels {
				model := modelsByID[entry.id]
				if model == nil {
					continue
				}
				compatible := entry.fallbacks
				if model.Object("compat").Bool("supportsMidConvoEffort") {
					compatible = nil
					for _, fallbackID := range entry.fallbacks {
						if SupportsClaudeMidConvoEffort(fallbackID) {
							compatible = append(compatible, fallbackID)
						}
					}
				}
				allowed := []any{}
				for _, fallbackID := range compatible {
					if fallback := modelsByID[fallbackID]; fallback != nil {
						allowed = append(allowed, O(
							"provider", fallback.Str("provider"),
							"model", fallback.Str("id"),
							"cost", fallback.Object("cost").Clone(),
						))
					}
				}
				if len(allowed) > 0 {
					MergeCompat(model, O("allowedFallbackModels", allowed))
				}
			}
		},
	}
}
