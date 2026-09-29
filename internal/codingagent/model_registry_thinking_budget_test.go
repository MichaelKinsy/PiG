package codingagent

import "testing"

func TestThinkingTokenBudgetFieldOverrideIsPreserved(t *testing.T) {
	base := &providerCompat{ThinkingTokenBudgetField: "thinking_budget_tokens", SupportsThinkingTokenBudget: new(true)}
	override := &providerCompat{ThinkingTokenBudgetField: "thinking_budget"}
	merged := mergeCompat(base, override)
	if merged.ThinkingTokenBudgetField != "thinking_budget" || merged.SupportsThinkingTokenBudget == nil || !*merged.SupportsThinkingTokenBudget {
		t.Fatalf("merged=%#v", merged)
	}
	if base.ThinkingTokenBudgetField != "thinking_budget_tokens" {
		t.Fatal("base metadata mutated")
	}
}
