package ai

import "testing"

// Pi: packages/ai/src/models.ts:1235-1254 clampThinkingLevel returns `availableLevels[0] ?? "off"` when no level is requested-compatible: a reasoning model whose
// thinkingLevelMap removes every level (null for each, xhigh and max being null or absent) has no supported level, so every request clamps to "off" instead of reading the empty list.
// mutation-checked: the mutant "ClampThinkingLevel does not fall back to off when no level is available" (model_utils.go:123) fails it.
func TestClampThinkingLevelFallsBackToOffWhenTheModelSupportsNoLevel(t *testing.T) {
	none := ThinkingLevelMap{}
	for _, level := range extendedThinkingLevels {
		none[level] = nil
	}
	model := &Model{ProviderMeta: ProviderMetadata{Reasoning: true}, ThinkingLevelMap: none}
	if got := GetSupportedThinkingLevels(model); len(got) != 0 {
		t.Fatalf("supported levels = %v, want none", got)
	}
	for _, level := range append(append([]ModelThinkingLevel{}, extendedThinkingLevels...), "bogus") {
		if got := ClampThinkingLevel(model, level); got != ThinkingOff {
			t.Errorf("ClampThinkingLevel(%q) = %q, want off", level, got)
		}
	}
}
