package providers

// Ports packages/ai/catalog/providers/amazon-bedrock.ts

import (
	"slices"
	"strings"

	. "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts
)

// Opus 5 is only callable through cross-region inference profiles.
var amazonBedrockInferenceProfileOnlyModelIDs = map[string]bool{"anthropic.claude-opus-5": true}

// Families whose AWS model cards document a flat `reasoning_effort` in Converse's
// additionalModelRequestFields. Converse forwards that field without validating it, and
// other families take effort elsewhere or not at all, so only these get models.dev effort levels.
// Do not add Nova 2 (reasoningConfig.maxReasoningEffort) or Grok (no Converse effort field),
// although models.dev lists effort values for both.
var amazonBedrockFlatReasoningEffortFamilies = []string{"moonshotai.kimi-k3", "zai.glm-5.3"}

// Kimi K3 and K3.1 fail on Converse with InternalServerException when reasoning from earlier
// turns is sent back; their AWS model cards say to drop it.
var amazonBedrockNoReasoningReplayFamilies = []string{"moonshotai.kimi-k3"}

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
					if slices.ContainsFunc(amazonBedrockFlatReasoningEffortFamilies, func(family string) bool {
						return strings.Contains(source.ID, family)
					}) {
						if thinkingLevelMap := GetEffortThinkingLevelMap(source.ReasoningOptions); thinkingLevelMap != nil {
							result.Set("thinkingLevelMap", thinkingLevelMap)
						}
					}
					compat := O()
					if source.StructuredOutput {
						compat.Set("supportsStrictMode", true)
					}
					if slices.ContainsFunc(amazonBedrockNoReasoningReplayFamilies, func(family string) bool {
						return strings.Contains(source.ID, family)
					}) {
						compat.Set("supportsReasoningReplay", false)
					}
					result.Set("compat", compat)
					return result, nil
				},
			})
		},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			WithInputLimits(model, O("images", O("maxPerMessage", 20)))
		},
	}
}
