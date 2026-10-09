package ai

import "testing"

// Ports packages/ai/src/api/simple-options.ts MIN_ANSWER_TOKENS and clampThinkingBudgetToAnswerRoom.
func TestClampThinkingBudgetToAnswerRoomLeavesMinAnswerTokens(t *testing.T) {
	if MinAnswerTokens != 1024 {
		t.Fatalf("MinAnswerTokens = %d, want 1024", MinAnswerTokens)
	}
	cases := []struct{ budget, ceiling, want int }{
		{16384, 100000, 16384},                // ample room: the budget stands
		{16384, 8192, 8192 - MinAnswerTokens}, // shared ceiling: answer room is kept
		{2048, MinAnswerTokens, 0},            // the ceiling is all answer room
		{2048, 10, 0},                         // ceiling below the minimum never goes negative
		{0, 5000, 0},
	}
	for _, c := range cases {
		if got := ClampThinkingBudgetToAnswerRoom(c.budget, c.ceiling); got != c.want {
			t.Errorf("ClampThinkingBudgetToAnswerRoom(%d, %d) = %d, want %d", c.budget, c.ceiling, got, c.want)
		}
	}
	// adjustMaxTokensForThinking applies it when the ceiling is not above the budget.
	base := 2000
	maxTokens, budget := AdjustMaxTokensForThinking(&base, 4096, "medium", nil)
	if maxTokens != 4096 || budget != 4096-MinAnswerTokens {
		t.Errorf("AdjustMaxTokensForThinking = %d, %d, want 4096, %d", maxTokens, budget, 4096-MinAnswerTokens)
	}
}
