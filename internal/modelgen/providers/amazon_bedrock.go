package providers

// Ports packages/ai/catalog/providers/amazon-bedrock.ts

import (
	"strings"

	. "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts
)

// Opus 5 is only callable through cross-region inference profiles.
var amazonBedrockInferenceProfileOnlyModelIDs = map[string]bool{"anthropic.claude-opus-5": true}

func amazonBedrockBaseURL(modelID string) string {
	if strings.HasPrefix(modelID, "eu.") {
		return "https://bedrock-runtime.eu-central-1.amazonaws.com"
	}
	return "https://bedrock-runtime.us-east-1.amazonaws.com"
}

func amazonBedrock() ProviderDefinition {
	return ProviderDefinition{
		ID: "amazon-bedrock",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "amazon-bedrock", ModelsDevOptions{
				API:     "bedrock-converse-stream",
				BaseURL: "",
				Include: func(source *ModelsDevModel) bool {
					return !amazonBedrockInferenceProfileOnlyModelIDs[source.ID] &&
						// Jamba does not support tool use in streaming mode.
						!strings.HasPrefix(source.ID, "ai21.jamba") &&
						// Mistral 7B Instruct does not support system messages.
						!strings.HasPrefix(source.ID, "mistral.mistral-7b-instruct-v0")
				},
				// Includes models.dev pricing tiers, e.g. the long-context tier for OpenAI models (#10326).
				Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
					result := Spread(model, O("baseUrl", amazonBedrockBaseURL(model.Str("id"))))
					if source.StructuredOutput {
						result.Set("compat", O("supportsStrictMode", true))
					}
					return result, nil
				},
			})
		},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			WithInputLimits(model, O("images", O("maxPerMessage", 20)))
		},
	}
}
