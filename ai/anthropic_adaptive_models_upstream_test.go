package ai

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Ports packages/ai/test/anthropic-adaptive-thinking-models.test.ts:33.
func TestAnthropicUpstreamAdaptiveThinkingModels(t *testing.T) {
	expected := []string{
		"anthropic/claude-fable-5", "anthropic/claude-opus-4-8", "anthropic/claude-opus-5", "anthropic/claude-sonnet-5", "cloudflare-ai-gateway/claude-fable-5",
		"fireworks/accounts/fireworks/models/deepseek-v4-flash-0731", "fireworks/accounts/fireworks/models/gpt-oss-120b", "fireworks/accounts/fireworks/models/qwen3p8-max",
		"kimi-coding/kimi-for-coding", "kimi-coding/k3", "kimi-coding/kimi-for-coding-highspeed", "opencode/claude-opus-4-8", "opencode/claude-opus-5",
		"vercel-ai-gateway/anthropic/claude-opus-4.8", "vercel-ai-gateway/anthropic/claude-opus-5", "vercel-ai-gateway/anthropic/claude-sonnet-5",
	}
	allowed := regexp.MustCompile(`(opus[-.](4[-.][678]|5)|sonnet[-.]4[-.]6|sonnet[-.]5|fable[-.]5|kimi-coding/)`)
	var flagged []string
	for _, model := range ListModels("") {
		if model.API != APIAnthropicMessages || model.Compat == nil || model.Compat.ForceAdaptiveThinking == nil || !*model.Compat.ForceAdaptiveThinking {
			continue
		}
		id := model.Provider + "/" + model.ID
		flagged = append(flagged, id)
		if !strings.HasPrefix(id, "fireworks/") && !allowed.MatchString(id) {
			t.Errorf("unexpected adaptive model %s", id)
		}
	}
	for _, id := range expected {
		if !slices.Contains(flagged, id) {
			t.Errorf("missing adaptive model %s", id)
		}
	}
}
