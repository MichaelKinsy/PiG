package providers

// Ports packages/ai/catalog/providers/minimax.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

// MiniMax asked to hide older models from its direct providers (#2596).
func minimaxHiddenModelID(id string) bool {
	switch id {
	case "MiniMax-M2", "MiniMax-M2.1", "MiniMax-M2.5", "MiniMax-M2.5-highspeed":
		return true
	}
	return false
}

// minimaxModels is MiniMax's Anthropic-compatible API. The SDK appends /v1/messages.
func minimaxModels(ctx *ProviderContext, source string, baseURL string) ([]ChatModel, error) {
	return ModelsDevModels(ctx, source, ModelsDevOptions{
		API:               "anthropic-messages",
		BaseURL:           baseURL,
		NoRecordReasoning: true,
		Include:           func(model *ModelsDevModel) bool { return !minimaxHiddenModelID(model.ID) },
		Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
			// Models with effort levels (e.g. MiniMax-M3.1-Flash) take adaptive thinking with
			// output_config.effort and reject thinking.type="disabled" (no "none" effort, so off is null).
			thinkingLevelMap := GetEffortThinkingLevelMap(source.ReasoningOptions)
			if thinkingLevelMap == nil {
				return model, nil
			}
			return Spread(model, O("thinkingLevelMap", thinkingLevelMap, "compat", O("forceAdaptiveThinking", true))), nil
		},
	})
}

func minimax() ProviderDefinition {
	return ProviderDefinition{
		ID: "minimax",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return minimaxModels(ctx, "minimax", "https://api.minimax.io/anthropic")
		},
	}
}
