package providers

// Ports packages/ai/catalog/providers/ant-ling.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func antLingCompat() *Obj {
	return O(
		"supportsStore", false,
		"supportsDeveloperRole", false,
		"supportsReasoningEffort", false,
		"maxTokensField", "max_tokens",
		"supportsLongCacheRetention", false,
	)
}

func antLingModel(id, name string, reasoning bool, cost *Obj) ChatModel {
	compat := antLingCompat()
	if reasoning {
		compat.Set("thinkingFormat", "ant-ling")
	}
	return O(
		"id", id,
		"name", name,
		"api", "openai-completions",
		"baseUrl", "https://api.ant-ling.com/v1",
		"provider", "ant-ling",
		"reasoning", reasoning,
		"input", Strings("text"),
		"cost", cost,
		"contextWindow", 262144,
		"maxTokens", 65536,
		"compat", compat,
	)
}

func antLing() ProviderDefinition {
	return ProviderDefinition{
		ID: "ant-ling",
		Models: func(*ProviderContext) ([]ChatModel, error) {
			return []ChatModel{
				antLingModel("Ling-2.6-flash", "Ling 2.6 Flash", false, O("input", 0.01, "output", 0.02, "cacheRead", 0, "cacheWrite", 0)),
				antLingModel("Ling-2.6-1T", "Ling 2.6 1T", false, O("input", 0.06, "output", 0.25, "cacheRead", 0, "cacheWrite", 0)),
				antLingModel("Ring-2.6-1T", "Ring 2.6 1T", true, O("input", 0.06, "output", 0.25, "cacheRead", 0, "cacheWrite", 0)),
			}, nil
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O(
			"supportsStore", false,
			"supportsDeveloperRole", false,
			"supportsReasoningEffort", false,
			"maxTokensField", "max_tokens",
			"thinkingFormat", "ant-ling",
			"supportsStrictMode", true,
			"supportsLongCacheRetention", false,
		))},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			// Ring reasons by default. Only high/xhigh have documented explicit effort controls.
			if model.Bool("reasoning") {
				MergeThinking(model, O("off", nil, "minimal", nil, "low", nil, "medium", nil, "high", "high", "xhigh", "xhigh"))
			}
		},
	}
}
