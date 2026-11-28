package ai

// Ports Pi v1.7.0 Ant Ling thinkingFormat deprecation tests from:
// - packages/ai/test/openai-completions-tool-choice.test.ts (9627ebab8, 1e7bc7213)

import (
	"reflect"
	"testing"
)

func TestAntLingDeprecatedThinkingFormatAlias(t *testing.T) {
	detected := DetectCompat("ant-ling", "https://api.ant-ling.com/v1")
	if detected.ThinkingFormat != "deepseek" {
		t.Fatalf("DetectCompat(ant-ling).ThinkingFormat = %q, want deepseek", detected.ThinkingFormat)
	}

	resolved := getOpenAICompletionsCompat("ant-ling", "https://api.ant-ling.com/v1", "Ling-3.1-flash", &OpenAICompat{
		ThinkingFormat: "ant-ling",
	})
	if resolved.ThinkingFormat != "deepseek" {
		t.Fatalf("getOpenAICompletionsCompat(ant-ling alias).ThinkingFormat = %q, want deepseek", resolved.ThinkingFormat)
	}

	deepseekModel := &Model{
		ID:          "Ling-3.1-flash",
		DisplayName: "Ling 3.1 Flash",
		ProviderMeta: ProviderMetadata{
			ProviderID: "ant-ling",
			API:        APIOpenAICompletions,
			BaseURL:    "https://api.ant-ling.com/v1",
			Reasoning:  true,
			Compat: &OpenAICompat{
				SupportsStore:              ptrBool(false),
				SupportsReasoningEffort:    ptrBool(false),
				MaxTokensField:             "max_tokens",
				ThinkingFormat:             "deepseek",
				SupportsStrictMode:         ptrBool(true),
				SupportsLongCacheRetention: ptrBool(false),
			},
		},
		ThinkingLevelMap: ThinkingLevelMap{
			ThinkingOff:     new("off"),
			ThinkingMinimal: nil,
			ThinkingLow:     nil,
			ThinkingMedium:  nil,
			ThinkingHigh:    new("high"),
			ThinkingXHigh:   nil,
			ThinkingMax:     nil,
		},
	}
	legacyModel := *deepseekModel
	legacyCompat := *deepseekModel.ProviderMeta.Compat
	legacyCompat.ThinkingFormat = "ant-ling"
	legacyModel.ProviderMeta.Compat = &legacyCompat

	for _, level := range []ThinkingLevel{"", ThinkingLevelHigh} {
		p1, _, _ := captureToolChoiceRequest(t, deepseekModel, toolChoiceHi(), StreamOptions{Thinking: level}, nil)
		p2, _, _ := captureToolChoiceRequest(t, &legacyModel, toolChoiceHi(), StreamOptions{Thinking: level}, nil)
		if !reflect.DeepEqual(p1, p2) {
			t.Fatalf("level=%q: legacy ant-ling payload %#v != deepseek payload %#v", level, p2, p1)
		}
	}
}
