package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/agent/src/agent.ts:198,473 onPayload is a public property copied into createLoopConfig once; agent-loop.ts passes it to every request of that run. A hook assigned while a run is in flight is first used by the next run.
func TestPayloadHookAssignedDuringARunAppliesToTheNextRun(t *testing.T) {
	var a *Agent
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c1", "swap", nil))}
	swap := &scriptTool{name: "swap", params: map[string]any{"type": "object"}, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		a.SetBeforeProviderHook(func(any, *ai.Model) (any, error) { return "new", nil })
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "swapped"}}}, nil
	}}
	a = mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{swap}})
	a.SetBeforeProviderHook(func(any, *ai.Model) (any, error) { return "old", nil })

	hookOf := func(call int) string {
		t.Helper()
		hook := provider.request(call).opts.OnPayload
		if hook == nil {
			return "none"
		}
		value, err := hook(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return value.(string)
	}
	mustSend(t, a, "one")
	if first, second := hookOf(1), hookOf(2); first != "old" || second != "old" {
		t.Fatalf("payload hooks of the run's requests = %q then %q, want the hook the run started with on both", first, second)
	}
	mustSend(t, a, "two")
	if got := hookOf(3); got != "new" {
		t.Fatalf("next run payload hook = %q, want new", got)
	}
}
