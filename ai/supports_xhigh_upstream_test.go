package ai

import (
	"reflect"
	"slices"
	"testing"
)

func TestSupportsXHighUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, model     string
		present, absent []ModelThinkingLevel
		exact           []ModelThinkingLevel
	}{
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:5
		{name: "includes max but not xhigh for Anthropic Opus 4.6 on anthropic-messages API", model: "anthropic/claude-opus-4-6", present: []ModelThinkingLevel{ThinkingMax}, absent: []ModelThinkingLevel{ThinkingXHigh}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:12
		{name: "includes xhigh and max for Anthropic Opus 4.8 on anthropic-messages API", model: "anthropic/claude-opus-4-8", present: []ModelThinkingLevel{ThinkingXHigh, ThinkingMax}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:19
		{name: "includes xhigh and max for Anthropic Opus 5 on anthropic-messages API", model: "anthropic/claude-opus-5", present: []ModelThinkingLevel{ThinkingXHigh, ThinkingMax}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:59 keeps this name but asserts toContain("xhigh"). The published
		// pi-ai 0.99.1 catalog gives claude-sonnet-4-6 thinkingLevelMap {"max":"max"}, and its getSupportedThinkingLevels returns
		// off, minimal, low, medium, high, max (Node 24), so that upstream case fails against Pi's shipped data. This row keeps
		// Pi's shipped behavior, which the 0.87.1 case (:42) asserted.
		{name: "includes max but not xhigh for Anthropic Sonnet 4.6 on anthropic-messages API", model: "anthropic/claude-sonnet-4-6", present: []ModelThinkingLevel{ThinkingMax}, absent: []ModelThinkingLevel{ThinkingXHigh}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:66
		{name: "includes xhigh and max for Anthropic Sonnet 5 on anthropic-messages API", model: "anthropic/claude-sonnet-5", present: []ModelThinkingLevel{ThinkingXHigh, ThinkingMax}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:73
		{name: "includes xhigh and max but not off for Anthropic Claude Fable 5 on anthropic-messages API", model: "anthropic/claude-fable-5", present: []ModelThinkingLevel{ThinkingXHigh, ThinkingMax}, absent: []ModelThinkingLevel{ThinkingOff}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:81
		{name: "does not include xhigh or max for Claude Sonnet 4.5", model: "anthropic/claude-sonnet-4-5", absent: []ModelThinkingLevel{ThinkingXHigh, ThinkingMax}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:160
		{name: "includes only medium/high/xhigh for OpenAI GPT-5.5 Pro", model: "openai/gpt-5.5-pro", exact: []ModelThinkingLevel{ThinkingMedium, ThinkingHigh, ThinkingXHigh}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:166
		{name: "includes only medium/high/xhigh for OpenRouter GPT-5.5 Pro", model: "openrouter/openai/gpt-5.5-pro", exact: []ModelThinkingLevel{ThinkingMedium, ThinkingHigh, ThinkingXHigh}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:172
		{name: "includes low/high/max plus off for DeepSeek V4.1 Flash on the DeepSeek provider", model: "deepseek/deepseek-flash", exact: []ModelThinkingLevel{ThinkingOff, ThinkingLow, ThinkingHigh, ThinkingMax}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:178
		{name: "includes low/high/max plus off for DeepSeek V4 Flash on opencode-go", model: "opencode-go/deepseek-v4-flash", exact: []ModelThinkingLevel{ThinkingOff, ThinkingLow, ThinkingHigh, ThinkingMax}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:184
		{name: "preserves low/high/max metadata for DeepSeek V4.1 Flash on OpenRouter", model: "openrouter/deepseek/deepseek-v4.1-flash", exact: []ModelThinkingLevel{ThinkingOff, ThinkingLow, ThinkingHigh, ThinkingMax}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:190
		{name: "preserves low/high/max metadata for DeepSeek V4.1 Flash on opencode-go", model: "opencode-go/deepseek-v4.1-flash", exact: []ModelThinkingLevel{ThinkingLow, ThinkingHigh, ThinkingMax}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:211
		{name: "includes only low, high, max for Kimi Coding K3", model: "kimi-coding/k3", exact: []ModelThinkingLevel{ThinkingLow, ThinkingHigh, ThinkingMax}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:217
		{name: "includes only high for OpenCode Grok Build", model: "opencode/grok-build-0.1", exact: []ModelThinkingLevel{ThinkingHigh}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:223
		{name: "includes only high/xhigh plus off for DeepSeek V4 Flash on OpenRouter", model: "openrouter/deepseek/deepseek-v4-flash", exact: []ModelThinkingLevel{ThinkingOff, ThinkingHigh, ThinkingXHigh}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:229
		{name: "includes max but not xhigh for OpenRouter Opus 4.6 (openai-completions API)", model: "openrouter/anthropic/claude-opus-4.6", present: []ModelThinkingLevel{ThinkingMax}, absent: []ModelThinkingLevel{ThinkingXHigh}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:236
		{name: "includes xhigh and max for Bedrock Claude Opus 5", model: "amazon-bedrock/global.anthropic.claude-opus-5", present: []ModelThinkingLevel{ThinkingXHigh, ThinkingMax}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:243
		{name: "includes xhigh but not off or max for xAI Grok 4.6", model: "xai/grok-4.6", exact: []ModelThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh}},
		// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:249
		{name: "includes xhigh and max but not off for Bedrock Claude Fable 5", model: "amazon-bedrock/global.anthropic.claude-fable-5", present: []ModelThinkingLevel{ThinkingXHigh, ThinkingMax}, absent: []ModelThinkingLevel{ThinkingOff}},
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
	// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:88
	for _, id := range []string{"gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol"} {
		t.Run("includes xhigh for openai-codex "+id+" models", func(t *testing.T) {
			if got := GetSupportedThinkingLevels(upstreamThinkingModel(t, "openai-codex/"+id)); !slices.Contains(got, ThinkingXHigh) {
				t.Fatalf("levels = %v", got)
			}
		})
	}
	// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:103
	for _, id := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-sol", "gpt-6-luna"} {
		t.Run("includes xhigh and max for OpenAI "+id+" models", func(t *testing.T) {
			want := []ModelThinkingLevel{ThinkingOff, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax}
			if got := GetSupportedThinkingLevels(upstreamThinkingModel(t, "openai/"+id)); !slices.Equal(got, want) {
				t.Fatalf("levels = %v, want %v", got, want)
			}
		})
	}
	// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:196
	t.Run("excludes thinking off for Moonshot Kimi K2.7 Code models", func(t *testing.T) {
		for _, provider := range []string{"moonshotai", "moonshotai-cn"} {
			want := []ModelThinkingLevel{ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh}
			if got := GetSupportedThinkingLevels(upstreamThinkingModel(t, provider+"/kimi-k2.7-code")); !slices.Equal(got, want) {
				t.Errorf("%s: levels = %v, want %v", provider, got, want)
			}
		}
	})
	// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:205
	for _, provider := range []string{"moonshotai", "moonshotai-cn"} {
		t.Run("uses the verified effort options for "+provider+" Kimi K3", func(t *testing.T) {
			want := []ModelThinkingLevel{ThinkingLow, ThinkingHigh, ThinkingMax}
			if got := GetSupportedThinkingLevels(upstreamThinkingModel(t, provider+"/kimi-k3")); !slices.Equal(got, want) {
				t.Fatalf("levels = %v, want %v", got, want)
			}
		})
	}
	// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:26
	t.Run("includes Claude Opus 5.5 with its always-on effort levels and official pricing", func(t *testing.T) {
		m := upstreamThinkingModel(t, "anthropic/claude-opus-5-5")
		want := []ModelThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax}
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
	// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:42
	t.Run("includes Claude Sonnet 5.5 with managed effort levels and official pricing", func(t *testing.T) {
		m := upstreamThinkingModel(t, "anthropic/claude-sonnet-5-5")
		if got := m.CostRates(); !reflect.DeepEqual(got, ModelCost{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5}) {
			t.Errorf("cost = %+v", got)
		}
		if m.Capabilities.ContextWindow != 1000000 || m.Capabilities.MaxOutputTokens != 128000 {
			t.Errorf("capabilities = %+v", m.Capabilities)
		}
		c := m.ProviderMeta.Compat
		if c == nil || c.ForceAdaptiveThinking == nil || !*c.ForceAdaptiveThinking || c.SupportsMidConvoEffort == nil || !*c.SupportsMidConvoEffort || c.SupportsMidConvoSystemMessages == nil || !*c.SupportsMidConvoSystemMessages || c.SupportsMidConvoToolChanges == nil || !*c.SupportsMidConvoToolChanges || c.SupportsTemperature == nil || *c.SupportsTemperature {
			t.Errorf("compat = %+v", c)
		}
		if got, want := GetSupportedThinkingLevels(m), []ModelThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax}; !slices.Equal(got, want) {
			t.Errorf("levels = %v, want %v", got, want)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:113 (OpenAI and Codex reject reasoning.effort "none" for GPT-6.1 Sol)
	t.Run("does not support off for GPT-6.1 Sol", func(t *testing.T) {
		for _, tc := range []struct {
			provider string
			levels   []ModelThinkingLevel
		}{
			{"openai", []ModelThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax}},
			{"azure", []ModelThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax}},
			{"openai-codex", []ModelThinkingLevel{ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax}},
		} {
			m := upstreamThinkingModel(t, tc.provider+"/gpt-6.1-sol")
			if got := GetSupportedThinkingLevels(m); !slices.Equal(got, tc.levels) {
				t.Errorf("%s levels = %v, want %v", tc.provider, got, tc.levels)
			}
			if off, ok := m.ThinkingLevelMap[ThinkingOff]; !ok || off != nil {
				t.Errorf("%s thinkingLevelMap.off = %v (present %t), want null", tc.provider, off, ok)
			}
		}
	})
	// .upstream/v0.99.1/packages/ai/test/supports-xhigh.test.ts:127
	for _, tc := range []struct {
		id   string
		cost ModelCost
	}{
		{"gpt-6-sol", ModelCost{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5}},
		{"gpt-6-luna", ModelCost{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125}},
		{"gpt-6.1-sol", ModelCost{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}},
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
