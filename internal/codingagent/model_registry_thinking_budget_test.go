package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

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

// provider-composer.ts mergeCompat: vercelGatewayRouting merges key by key, so an override that sets only `order` keeps the base `only`.
func TestMergeCompatMergesVercelGatewayRoutingKeyByKey(t *testing.T) {
	base := &providerCompat{VercelGatewayRouting: &ai.VercelGatewayRouting{Only: []string{"bedrock"}, Order: []string{"a"}}}
	override := &providerCompat{VercelGatewayRouting: &ai.VercelGatewayRouting{Order: []string{"b", "c"}}}
	merged := mergeCompat(base, override)
	routing := merged.VercelGatewayRouting
	if routing == nil || !slices.Equal(routing.Only, []string{"bedrock"}) || !slices.Equal(routing.Order, []string{"b", "c"}) {
		t.Fatalf("merged routing = %#v", routing)
	}
	routing.Only[0] = "mutated"
	if base.VercelGatewayRouting.Only[0] != "bedrock" {
		t.Fatal("merge aliased the base list")
	}
	if got := mergeCompat(&providerCompat{}, &providerCompat{}).VercelGatewayRouting; got != nil {
		t.Fatalf("no routing on either side merged to %#v", got)
	}
}
