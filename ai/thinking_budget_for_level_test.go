package ai

import "testing"

// upstream: packages/ai/src/api/simple-options.ts:81 thinkingBudgetForLevel merges custom budgets over the defaults and
// clamps xhigh and max to high.
func TestThinkingBudgetForLevel(t *testing.T) {
	custom := &ThinkingBudgets{Low: 3000, High: 20000}
	for _, tc := range []struct {
		level  string
		custom *ThinkingBudgets
		want   int
	}{
		{"minimal", nil, 1024},
		{"low", nil, 2048},
		{"medium", nil, 8192},
		{"high", nil, 16384},
		{"xhigh", nil, 16384},
		{"max", nil, 16384},
		{"low", custom, 3000},
		{"medium", custom, 8192},
		{"high", custom, 20000},
		{"xhigh", custom, 20000},
		{"max", custom, 20000},
	} {
		if got := ThinkingBudgetForLevel(tc.level, tc.custom); got != tc.want {
			t.Errorf("ThinkingBudgetForLevel(%q, %+v) = %d, want %d", tc.level, tc.custom, got, tc.want)
		}
	}
}

// upstream: simple-options.ts:98 adjustMaxTokensForThinking sizes the thinking budget through thinkingBudgetForLevel.
func TestAdjustMaxTokensForThinkingUsesLevelBudget(t *testing.T) {
	base := 4000
	maxTokens, budget := AdjustMaxTokensForThinking(&base, 100000, "max", &ThinkingBudgets{High: 20000})
	if budget != 20000 || maxTokens != 24000 {
		t.Fatalf("maxTokens, budget = %d, %d; want 24000, 20000", maxTokens, budget)
	}
}
