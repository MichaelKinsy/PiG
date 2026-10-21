package ai

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Ports packages/ai/test/anthropic-adaptive-thinking-models.test.ts (1.1.0 adds anthropic/claude-haiku-5-5 to the expected list and haiku[-.]5[-.]5 to the allowed pattern; Pi d2e7d8b7 adds minimax/ and minimax-cn/ MiniMax-M3.1-Flash and allows the minimax/ and minimax-cn/ prefixes, #11121).
func TestAnthropicUpstreamAdaptiveThinkingModels(t *testing.T) {
	expected := []string{
		"anthropic/claude-fable-5", "anthropic/claude-haiku-5-5", "anthropic/claude-opus-4-8", "anthropic/claude-opus-5", "anthropic/claude-sonnet-5", "cloudflare-ai-gateway/claude-fable-5",
		"fireworks/accounts/fireworks/models/deepseek-v4p1-flash", "fireworks/accounts/fireworks/models/gpt-oss-120b", "fireworks/accounts/fireworks/models/qwen3p8-max",
		"minimax-cn/MiniMax-M3.1-Flash", "minimax/MiniMax-M3.1-Flash",
		"opencode/claude-opus-4-8", "opencode/claude-opus-5",
		"vercel-ai-gateway/anthropic/claude-opus-4.8", "vercel-ai-gateway/anthropic/claude-opus-5", "vercel-ai-gateway/anthropic/claude-sonnet-5",
	}
	allowed := regexp.MustCompile(`(opus[-.](4[-.][678]|5)|sonnet[-.]4[-.]6|sonnet[-.]5|haiku[-.]5[-.]5|fable[-.]5)`)
	var flagged []string
	for _, model := range ListModels("") {
		if model.API != APIAnthropicMessages || model.Compat == nil || model.Compat.ForceAdaptiveThinking == nil || !*model.Compat.ForceAdaptiveThinking {
			continue
		}
		id := model.Provider + "/" + model.ID
		flagged = append(flagged, id)
		// #11121: MiniMax models with catalog effort levels require adaptive thinking.
		if !strings.HasPrefix(id, "fireworks/") && !strings.HasPrefix(id, "minimax/") && !strings.HasPrefix(id, "minimax-cn/") && !allowed.MatchString(id) {
			t.Errorf("unexpected adaptive model %s", id)
		}
	}
	for _, id := range expected {
		if !slices.Contains(flagged, id) {
			t.Errorf("missing adaptive model %s", id)
		}
	}
}
