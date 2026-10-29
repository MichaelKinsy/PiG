package providers

// Ports packages/ai/catalog/providers/deepseek.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

// models.dev's DeepSeek entries lag the API; pricing from https://api-docs.deepseek.com/quick_start/pricing. DeepSeek also offers
// time-based off-peak rates, which the cost schema cannot represent yet.
func deepseekModels() []ChatModel {
	return []ChatModel{
		O(
			"id", "deepseek-flash",
			"name", "DeepSeek V4.1 Flash",
			"api", "openai-completions",
			"baseUrl", "https://api.deepseek.com",
			"provider", "deepseek",
			"reasoning", true,
			"thinkingLevelMap", DeepSeekV4FlashThinkingLevelMap(),
			"input", Strings("text", "image"),
			"cost", O("input", 0.3, "output", 1.2, "cacheRead", 0.006, "cacheWrite", 0),
			"contextWindow", 1000000,
			"maxTokens", 384000,
			"compat", DeepSeekCompat(),
		),
		O(
			"id", "deepseek-v4-pro",
			"name", "DeepSeek V4 Pro",
			"api", "openai-completions",
			"baseUrl", "https://api.deepseek.com",
			"provider", "deepseek",
			"reasoning", true,
			"input", Strings("text"),
			"cost", O("input", 1.32, "output", 3.96, "cacheRead", 0.044, "cacheWrite", 0),
			"contextWindow", 1000000,
			"maxTokens", 384000,
			"compat", DeepSeekCompat(),
		),
	}
}

func deepseek() ProviderDefinition {
	return ProviderDefinition{
		ID:     "deepseek",
		Models: func(*ProviderContext) ([]ChatModel, error) { return deepseekModels(), nil },
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O(
			"supportsStore", false,
			"supportsDeveloperRole", false,
			"maxTokensField", "max_tokens",
			"requiresReasoningContentOnAssistantMessages", true,
			"thinkingFormat", "deepseek",
			"supportsStrictMode", true,
		))},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			// DeepSeek V4 Pro accepts plain system text in place mid-conversation.
			if model.Str("id") == "deepseek-v4-pro" {
				MergeCompat(model, O("supportsMidConvoSystemMessages", true))
			}
		},
	}
}
