package modelgen

// Ports packages/ai/catalog/families/deepseek.ts: DeepSeek V4 models on OpenAI-compatible hosts.

import "strings"

// DeepSeekCompat is the compat of DeepSeek V4 on its own API and on hosts that take DeepSeek's thinking format.
func DeepSeekCompat() *Obj {
	return O("requiresReasoningContentOnAssistantMessages", true, "thinkingFormat", "deepseek")
}

// DeepSeekV4ThinkingLevelMap is the V4 Pro level map.
func DeepSeekV4ThinkingLevelMap() *Obj {
	return O("minimal", nil, "low", nil, "medium", nil, "high", "high", "max", "max")
}

// DeepSeekV4FlashThinkingLevelMap is the V4 Flash level map: V4 Pro's with low.
func DeepSeekV4FlashThinkingLevelMap() *Obj {
	return Spread(DeepSeekV4ThinkingLevelMap(), O("low", "low"))
}

// Azure Foundry rejects DeepSeek's own max effort.
func azureDeepSeekV4ThinkingLevelMap() *Obj {
	return O("minimal", nil, "low", "low", "medium", "medium", "high", "high", "xhigh", nil, "max", nil)
}

// Qwen Token Plan serves DeepSeek V4 through Qwen's own thinking format.
var qwenFormatProviders = map[string]bool{"qwen-token-plan": true, "qwen-token-plan-cn": true, "qwen-token-plan-individual": true}

// These hosts accept native reasoning efforts instead of DeepSeek's thinking toggle.
var nativeEffortProviders = map[string]bool{"openrouter": true, "opencode": true}

func isDeepSeekV4(model ChatModel) bool {
	return model.Str("api") == "openai-completions" && strings.Contains(model.Str("id"), "deepseek-v4")
}

// applyDeepSeekCompatRules: DeepSeek V4 needs its reasoning content replayed on every host.
func applyDeepSeekCompatRules(model ChatModel) {
	if !isDeepSeekV4(model) || qwenFormatProviders[model.Str("provider")] {
		return
	}
	if nativeEffortProviders[model.Str("provider")] {
		MergeCompat(model, O("requiresReasoningContentOnAssistantMessages", true))
		return
	}
	MergeCompat(model, DeepSeekCompat())
}

// applyDeepSeekFamilyRules sets DeepSeek V4 effort levels when the host did not report its own.
func applyDeepSeekFamilyRules(model ChatModel) {
	if !isDeepSeekV4(model) || model.Has("thinkingLevelMap") {
		return
	}
	provider, id := model.Str("provider"), model.Str("id")
	switch {
	case provider == "openrouter":
		MergeThinking(model, Spread(DeepSeekV4ThinkingLevelMap(), O("xhigh", "xhigh", "max", nil)))
	case provider == "azure":
		MergeThinking(model, azureDeepSeekV4ThinkingLevelMap())
	case (provider == "deepseek" || provider == "opencode" || provider == "opencode-go") && strings.Contains(id, "deepseek-v4-flash"):
		MergeThinking(model, DeepSeekV4FlashThinkingLevelMap())
	default:
		MergeThinking(model, DeepSeekV4ThinkingLevelMap())
	}
}
