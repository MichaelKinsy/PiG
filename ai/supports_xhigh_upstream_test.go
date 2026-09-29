package ai

import (
	"reflect"
	"slices"
	"testing"
)

func TestSupportsXHighUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, model     string
		present, absent []ThinkingLevel
		exact           []ThinkingLevel
	}{
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:5
		{name: "includes max but not xhigh for Anthropic Opus 4.6 on anthropic-messages API", model: "anthropic/claude-opus-4-6", present: []ThinkingLevel{ThinkingMax}, absent: []ThinkingLevel{ThinkingXHigh}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:12
		{name: "includes xhigh and max for Anthropic Opus 4.8 on anthropic-messages API", model: "anthropic/claude-opus-4-8", present: []ThinkingLevel{ThinkingXHigh, ThinkingMax}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:19
		{name: "includes xhigh and max for Anthropic Opus 5 on anthropic-messages API", model: "anthropic/claude-opus-5", present: []ThinkingLevel{ThinkingXHigh, ThinkingMax}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:42
		{name: "includes max but not xhigh for Anthropic Sonnet 4.6 on anthropic-messages API", model: "anthropic/claude-sonnet-4-6", present: []ThinkingLevel{ThinkingMax}, absent: []ThinkingLevel{ThinkingXHigh}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:49
		{name: "includes xhigh and max for Anthropic Sonnet 5 on anthropic-messages API", model: "anthropic/claude-sonnet-5", present: []ThinkingLevel{ThinkingXHigh, ThinkingMax}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:56
		{name: "includes xhigh and max but not off for Anthropic Claude Fable 5 on anthropic-messages API", model: "anthropic/claude-fable-5", present: []ThinkingLevel{ThinkingXHigh, ThinkingMax}, absent: []ThinkingLevel{ThinkingOff}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:64
		{name: "does not include xhigh or max for Claude Sonnet 4.5", model: "anthropic/claude-sonnet-4-5", absent: []ThinkingLevel{ThinkingXHigh, ThinkingMax}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:126
		{name: "includes only medium/high/xhigh for OpenAI GPT-5.5 Pro", model: "openai/gpt-5.5-pro", exact: []ThinkingLevel{ThinkingMedium, ThinkingHigh, ThinkingXHigh}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:132
		{name: "includes only medium/high/xhigh for OpenRouter GPT-5.5 Pro", model: "openrouter/openai/gpt-5.5-pro", exact: []ThinkingLevel{ThinkingMedium, ThinkingHigh, ThinkingXHigh}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:138
		{name: "includes low/high/max plus off for DeepSeek V4.1 Flash on the DeepSeek provider", model: "deepseek/deepseek-flash", exact: []ThinkingLevel{ThinkingOff, ThinkingLow, ThinkingHigh, ThinkingMax}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:144
		{name: "includes low/high/max plus off for DeepSeek V4 Flash on opencode-go", model: "opencode-go/deepseek-v4-flash", exact: []ThinkingLevel{ThinkingOff, ThinkingLow, ThinkingHigh, ThinkingMax}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:150
		{name: "preserves low/high/max metadata for DeepSeek V4.1 Flash on OpenRouter", model: "openrouter/deepseek/deepseek-v4.1-flash", exact: []ThinkingLevel{ThinkingOff, ThinkingLow, ThinkingHigh, ThinkingMax}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:156
		{name: "preserves low/high/max metadata for DeepSeek V4.1 Flash on opencode-go", model: "opencode-go/deepseek-v4.1-flash", exact: []ThinkingLevel{ThinkingLow, ThinkingHigh, ThinkingMax}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:162
		{name: "includes only high plus off for OpenCode Go Kimi K2.6", model: "opencode-go/kimi-k2.6", exact: []ThinkingLevel{ThinkingOff, ThinkingHigh}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:183
		{name: "includes only low, high, max for Kimi Coding K3", model: "kimi-coding/k3", exact: []ThinkingLevel{ThinkingLow, ThinkingHigh, ThinkingMax}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:189
		{name: "includes only high for OpenCode Grok Build", model: "opencode/grok-build-0.1", exact: []ThinkingLevel{ThinkingHigh}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:195
		{name: "includes only high/xhigh plus off for DeepSeek V4 Flash on OpenRouter", model: "openrouter/deepseek/deepseek-v4-flash", exact: []ThinkingLevel{ThinkingOff, ThinkingHigh, ThinkingXHigh}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:201
		{name: "includes max but not xhigh for OpenRouter Opus 4.6 (openai-completions API)", model: "openrouter/anthropic/claude-opus-4.6", present: []ThinkingLevel{ThinkingMax}, absent: []ThinkingLevel{ThinkingXHigh}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:208
		{name: "includes xhigh and max for Bedrock Claude Opus 5", model: "amazon-bedrock/global.anthropic.claude-opus-5", present: []ThinkingLevel{ThinkingXHigh, ThinkingMax}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:215
		{name: "includes xhigh but not off or max for xAI Grok 4.6", model: "xai/grok-4.6", exact: []ThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh}},
		// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:221
		{name: "includes xhigh and max but not off for Bedrock Claude Fable 5", model: "amazon-bedrock/global.anthropic.claude-fable-5", present: []ThinkingLevel{ThinkingXHigh, ThinkingMax}, absent: []ThinkingLevel{ThinkingOff}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := GetSupportedThinkingLevels(upstreamThinkingModel(t, tc.model))
			if tc.exact != nil && !slices.Equal(got, tc.exact) {
				t.Fatalf("levels = %v, want %v", got, tc.exact)
			}
			for _, level := range tc.present {
				if !slices.Contains(got, level) {
					t.Errorf("levels %v lack %s", got, level)
				}
			}
			for _, level := range tc.absent {
				if slices.Contains(got, level) {
					t.Errorf("levels %v contain %s", got, level)
				}
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:71
	for _, id := range []string{"gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna"} {
		t.Run("includes xhigh for openai-codex "+id+" models", func(t *testing.T) {
			if got := GetSupportedThinkingLevels(upstreamThinkingModel(t, "openai-codex/"+id)); !slices.Contains(got, ThinkingXHigh) {
				t.Fatalf("levels = %v", got)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:85
	for _, id := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-sol", "gpt-6-luna"} {
		t.Run("includes xhigh and max for OpenAI "+id+" models", func(t *testing.T) {
			want := []ThinkingLevel{ThinkingOff, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax}
			if got := GetSupportedThinkingLevels(upstreamThinkingModel(t, "openai/"+id)); !slices.Equal(got, want) {
				t.Fatalf("levels = %v, want %v", got, want)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:168
	t.Run("excludes thinking off for Moonshot Kimi K2.7 Code models", func(t *testing.T) {
		for _, provider := range []string{"moonshotai", "moonshotai-cn"} {
			want := []ThinkingLevel{ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh}
			if got := GetSupportedThinkingLevels(upstreamThinkingModel(t, provider+"/kimi-k2.7-code")); !slices.Equal(got, want) {
				t.Errorf("%s: levels = %v, want %v", provider, got, want)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:177
	for _, provider := range []string{"moonshotai", "moonshotai-cn"} {
		t.Run("uses the verified effort options for "+provider+" Kimi K3", func(t *testing.T) {
			want := []ThinkingLevel{ThinkingLow, ThinkingHigh, ThinkingMax}
			if got := GetSupportedThinkingLevels(upstreamThinkingModel(t, provider+"/kimi-k3")); !slices.Equal(got, want) {
				t.Fatalf("levels = %v, want %v", got, want)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:26
	t.Run("includes Claude Opus 5.5 with its always-on effort levels and official pricing", func(t *testing.T) {
		m := upstreamThinkingModel(t, "anthropic/claude-opus-5-5")
		want := []ThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax}
		if got := GetSupportedThinkingLevels(m); !slices.Equal(got, want) {
			t.Errorf("levels = %v, want %v", got, want)
		}
		if got := m.CostRates(); !reflect.DeepEqual(got, ModelCost{Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5}) {
			t.Errorf("cost = %+v", got)
		}
		if m.Capabilities.ContextWindow != 1000000 || m.Capabilities.MaxOutputTokens != 128000 {
			t.Errorf("capabilities = %+v", m.Capabilities)
		}
		c := m.ProviderMeta.Compat
		if c == nil || c.ForceAdaptiveThinking == nil || !*c.ForceAdaptiveThinking || c.SupportsMidConvoEffort == nil || !*c.SupportsMidConvoEffort || c.SupportsMidConvoSystemMessages == nil || !*c.SupportsMidConvoSystemMessages || c.SupportsMidConvoToolChanges == nil || !*c.SupportsMidConvoToolChanges {
			t.Errorf("compat = %+v", c)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/supports-xhigh.test.ts:94
	for _, tc := range []struct {
		id   string
		cost ModelCost
	}{
		{"gpt-6-sol", ModelCost{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5}},
		{"gpt-6-luna", ModelCost{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125}},
	} {
		t.Run("includes official metadata for OpenAI and Codex "+tc.id, func(t *testing.T) {
			for _, provider := range []string{"openai", "openai-codex"} {
				m := upstreamThinkingModel(t, provider+"/"+tc.id)
				cost := tc.cost
				cost.Tiers = []CostTier{{InputTokensAbove: 272000, InputCostPer1M: cost.Input * 2, OutputCostPer1M: cost.Output * 1.5, CacheReadCostPer1M: cost.CacheRead * 2, CacheWriteCostPer1M: cost.CacheWrite * 2}}
				if got := m.CostRates(); !reflect.DeepEqual(got, cost) {
					t.Errorf("%s cost = %+v, want %+v", provider, got, cost)
				}
				if !slices.Equal(m.Input, []string{"text", "image"}) || m.Capabilities.ContextWindow != 272000 || m.Capabilities.MaxOutputTokens != 128000 {
					t.Errorf("%s model = %+v", provider, m)
				}
				c := m.ProviderMeta.Compat
				if c == nil || c.SupportsAdditionalTools == nil || !*c.SupportsAdditionalTools || c.SupportsMidConvoSystemMessages == nil || !*c.SupportsMidConvoSystemMessages || c.SupportsOpenAIGrammarTools == nil || !*c.SupportsOpenAIGrammarTools || c.SupportsToolSearch == nil || !*c.SupportsToolSearch {
					t.Errorf("%s compat = %+v", provider, c)
				}
			}
		})
	}
}

func upstreamThinkingModel(t *testing.T, spec string) *Model {
	t.Helper()
	generated, ok := LookupModelExact(spec)
	if !ok {
		t.Fatalf("model %s is missing", spec)
	}
	return generated.ToModel()
}
