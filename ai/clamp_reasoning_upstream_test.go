package ai

import "testing"

// Pi simple-options.ts:77 clampReasoning: xhigh and max become high; every other level, and undefined, are unchanged.
func TestClampReasoningUpstream(t *testing.T) {
	for in, want := range map[ModelThinkingLevel]ModelThinkingLevel{
		"": "", ThinkingOff: ThinkingOff, ThinkingMinimal: ThinkingMinimal, ThinkingLow: ThinkingLow,
		ThinkingMedium: ThinkingMedium, ThinkingHigh: ThinkingHigh, ThinkingXHigh: ThinkingHigh, ThinkingMax: ThinkingHigh,
	} {
		if got := ClampReasoning(in); got != want {
			t.Errorf("ClampReasoning(%q) = %q, want %q", in, got, want)
		}
	}
	// thinkingBudgetForLevel / adjustMaxTokensForThinking use the clamped level: xhigh and max take the high budget.
	_, high := AdjustMaxTokensForThinking(nil, 100000, "high", nil)
	for _, level := range []string{"xhigh", "max"} {
		if _, got := AdjustMaxTokensForThinking(nil, 100000, level, nil); got != high {
			t.Errorf("%s budget = %d, want the high budget %d", level, got, high)
		}
	}
}
