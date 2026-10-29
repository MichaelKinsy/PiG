package modelgen

// Ports packages/ai/catalog/families/kimi.ts: Moonshot Kimi models, shared by Moonshot, Kimi Coding, and gateways.

import "regexp"

var kimiK3Family = regexp.MustCompile(`^kimi-k3(?:\.\d+)?(?:-|$)`)

// IsKimiK3FamilyID: Kimi K3 and its point releases (kimi-k3.1-pro, kimi-k3.1-pro-highspeed) share K3's request format.
// https://api.moonshot.ai/v1/models reports effort levels and dynamic tool loading for all of them.
func IsKimiK3FamilyID(modelID string) bool { return kimiK3Family.MatchString(modelID) }

// KimiK3MaxTokens is the K3 family's canonical output limit.
const KimiK3MaxTokens = 131072

// KimiK3Cost is Kimi K3's price.
func KimiK3Cost() *Obj {
	return O("input", 3, "output", 15, "cacheRead", 0.3, "cacheWrite", 0)
}

// KimiOpenAICompat is the Kimi Chat Completions settings shared by Moonshot and Kimi Coding.
func KimiOpenAICompat() *Obj {
	return O(
		"supportsStore", false,
		"supportsDeveloperRole", false,
		"supportsReasoningEffort", false,
		"maxTokensField", "max_tokens",
		"supportsStrictMode", false,
		"thinkingFormat", "deepseek",
	)
}

// KimiK3OpenAICompat: Kimi K3-style models take reasoning_effort and need earlier reasoning sent back.
func KimiK3OpenAICompat() *Obj {
	return Spread(KimiOpenAICompat(), O(
		"requiresReasoningContentOnAssistantMessages", true,
		"thinkingFormat", "openai",
		"supportsReasoningEffort", true,
	))
}
