package ai

import "testing"

// Ports the usesGoogleThinkingLevel model-id rules of packages/ai/src/api/google-shared.ts: the id is lowercased before every comparison, so the "-latest" aliases match in any case.
func TestUsesGoogleThinkingLevelMatchesPiModelIds(t *testing.T) {
	for id, want := range map[string]bool{
		"gemini-3-flash-preview":      true,
		"gemini-3.1-pro-preview":      true,
		"gemini-3.8-flash":            true,
		"Gemini-3.1-Pro-Preview":      true,
		"gemini-flash-latest":         true,
		"Gemini-Flash-Latest":         true,
		"GEMINI-FLASH-LITE-LATEST":    true,
		"gemma-4-31b-it":              true,
		"Gemma4-27b":                  true,
		"gemini-2.5-flash":            false,
		"gemini-3-ultra":              false,
		"gemini-flash-latest-preview": false,
		"":                            false,
	} {
		if got := UsesGoogleThinkingLevel(&Model{ID: id}); got != want {
			t.Errorf("UsesGoogleThinkingLevel(%q) = %v, want %v", id, got, want)
		}
	}
}

// A mixed-case alias selects the discrete level wire format, as it does for the lowercase id.
func TestBuildGeminiThinkingConfigUsesLevelForMixedCaseLatestAlias(t *testing.T) {
	for _, id := range []string{"gemini-flash-latest", "Gemini-Flash-Latest"} {
		model := &Model{ID: id, ProviderMeta: ProviderMetadata{ProviderID: "test-google", Reasoning: true}}
		config, err := buildGeminiThinkingConfig(model, ThinkingHigh, true, nil)
		if err != nil || config == nil || config.ThinkingLevel != "HIGH" || config.ThinkingBudget != nil {
			t.Errorf("%s: config = %+v, %v; want ThinkingLevel HIGH and no budget", id, config, err)
		}
	}
}
