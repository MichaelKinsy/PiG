package providers

// Ports packages/ai/catalog/providers/google.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

// googleAliasTargets are rolling aliases, which take their metadata from the model they currently point to.
func googleAliasTargets() map[string]string {
	return map[string]string{
		"gemini-flash-latest":      "gemini-3.5-flash",
		"gemini-flash-lite-latest": "gemini-3.1-flash-lite",
	}
}

// googleOr is `value || fallback` on an optional models.dev price.
func googleOr(value *float64, fallback float64) float64 {
	if value == nil || *value == 0 || *value != *value {
		return fallback
	}
	return *value
}

func googleModelsDevByID(ctx *ProviderContext, provider string) map[string]*ModelsDevModel {
	sources := map[string]*ModelsDevModel{}
	for _, model := range ModelsDevChatModels(ctx.Sources.ModelsDev, provider) {
		sources[model.ID] = model
	}
	return sources
}

func googleOrNumber(value, fallback float64) float64 {
	if value == 0 || value != value {
		return fallback
	}
	return value
}

func google() ProviderDefinition {
	return ProviderDefinition{
		ID: "google",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			sources := googleModelsDevByID(ctx, "google")
			aliases := googleAliasTargets()
			return ModelsDevModels(ctx, "google", ModelsDevOptions{
				API:               "google-generative-ai",
				BaseURL:           "https://generativelanguage.googleapis.com/v1beta",
				NoRecordReasoning: true,
				Map: func(model ChatModel, aliasSource *ModelsDevModel) (ChatModel, error) {
					source := aliasSource
					if target := aliases[model.Str("id")]; target != "" {
						if found := sources[target]; found != nil {
							source = found
						}
					}
					thinkingLevelMap := GoogleThinkingLevels(model.Str("id"), source.ReasoningOptions)
					result := Spread(model, O("reasoning", source.Reasoning))
					if thinkingLevelMap != nil {
						result.Set("thinkingLevelMap", thinkingLevelMap)
					}
					if source.ImageInput {
						result.Set("input", Strings("text", "image"))
					} else {
						result.Set("input", Strings("text"))
					}
					result.Set("cost", ModelsDevToCost(source.Cost))
					result.Set("contextWindow", googleOrNumber(source.ContextWindow, 4096))
					result.Set("maxTokens", googleOrNumber(source.MaxTokens, 4096))
					return result, nil
				},
			})
		},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			WithInputLimits(model, O("maxRequestBytes", 20*1024*1024, "images", O("maxPerRequest", 3600)))
		},
	}
}
