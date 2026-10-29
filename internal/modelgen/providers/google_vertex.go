package providers

// Ports packages/ai/catalog/providers/google-vertex.ts

import (
	"strings"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

func googleVertex() ProviderDefinition {
	return ProviderDefinition{
		ID: "google-vertex",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			sources := googleModelsDevByID(ctx, "google-vertex")
			aliases := googleAliasTargets()
			return ModelsDevModels(ctx, "google-vertex", ModelsDevOptions{
				API:               "google-vertex",
				BaseURL:           "https://{location}-aiplatform.googleapis.com",
				NoRecordReasoning: true,
				// The models.dev catalog also includes Claude, OpenAI, and other MaaS models
				// that do not use the @google/genai Gemini streaming path.
				Include: func(source *ModelsDevModel) bool {
					return strings.HasPrefix(source.ID, "gemini-") && source.ID != "gemini-3.1-flash-lite-preview"
				},
				Map: func(model ChatModel, aliasSource *ModelsDevModel) (ChatModel, error) {
					id := model.Str("id")
					source := aliasSource
					if target := aliases[id]; target != "" {
						if found := sources[target]; found != nil {
							source = found
						}
					}
					thinkingLevelMap := GoogleThinkingLevels(id, source.ReasoningOptions)
					result := Spread(model, O("reasoning", source.Reasoning))
					if thinkingLevelMap != nil {
						result.Set("thinkingLevelMap", thinkingLevelMap)
					}
					if source.ImageInput {
						result.Set("input", Strings("text", "image"))
					} else {
						result.Set("input", Strings("text"))
					}
					// models.dev reports Vertex cache_read/cache_write values for Gemini 2.5 Flash that
					// do not match the official Gemini API standard pricing table. pi only accounts
					// cachedContentTokenCount as cacheRead.
					cacheRead := googleOr(source.Cost.CacheRead, 0)
					if id == "gemini-2.5-flash" {
						cacheRead = 0.03
					}
					result.Set("cost", O(
						"input", googleOr(source.Cost.Input, 0),
						"output", googleOr(source.Cost.Output, 0),
						"cacheRead", cacheRead,
						"cacheWrite", 0,
					))
					result.Set("contextWindow", googleOrNumber(source.ContextWindow, 4096))
					result.Set("maxTokens", googleOrNumber(source.MaxTokens, 4096))
					return result, nil
				},
			})
		},
	}
}
