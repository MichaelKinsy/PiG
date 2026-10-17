package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/agent/src/agent.ts:471 createLoopConfig copies `model: this._state.model` and `reasoning` into the run's config once, and agent-loop.ts:403 streams every request of the run with that config.model. Assigning state.model during a run (from a tool here) does not change the run's later requests; the next run starts with the assigned model. Only prepareNextTurn/prepareRequest updates (agent-loop.ts:192,231) change the model mid-run.
func TestModelAssignedDuringARunAppliesToTheNextRun(t *testing.T) {
	var a *Agent
	first := &scriptedProvider{id: "first", respond: toolCallsThenText(toolCall("c1", "swap", nil))}
	second := &scriptedProvider{id: "second", respond: replyText("from second")}
	swap := &scriptTool{name: "swap", params: map[string]any{"type": "object"}, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		a.SetModel(scriptedModel(second))
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "swapped"}}}, nil
	}}
	a = mustNewAgent(AgentOptions{Model: scriptedModel(first), Tools: []AgentTool{swap}})

	mustSend(t, a, "one")
	if got, want := first.calls(), 2; got != want {
		t.Fatalf("first provider requests = %d, want %d: both requests of the run use the model it started with", got, want)
	}
	if got := second.calls(); got != 0 {
		t.Fatalf("second provider requests = %d during the run that assigned it, want 0", got)
	}
	mustSend(t, a, "two")
	if got := second.calls(); got != 1 {
		t.Fatalf("second provider requests after the next run = %d, want 1", got)
	}
}

// agent.ts:472 `reasoning: this._state.thinkingLevel === "off" ? undefined : ...` is copied once as well: a thinking level assigned during a run applies to the next run's requests.
func TestThinkingLevelAssignedDuringARunAppliesToTheNextRun(t *testing.T) {
	var a *Agent
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c1", "swap", nil))}
	swap := &scriptTool{name: "swap", params: map[string]any{"type": "object"}, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		a.SetThinkingLevel(ai.ThinkingHigh)
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "swapped"}}}, nil
	}}
	a = mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{swap}, ThinkingLevel: ai.ThinkingOff})
	mustSend(t, a, "one")
	if got := provider.request(2).opts.Thinking; got != ai.ThinkingOff.ReasoningOption() {
		t.Fatalf("second request of the run thinking = %q, want the level the run started with (%q)", got, ai.ThinkingOff.ReasoningOption())
	}
	mustSend(t, a, "two")
	if got := provider.request(3).opts.Thinking; got != ai.ThinkingHigh.ReasoningOption() {
		t.Fatalf("next run thinking = %q, want %q", got, ai.ThinkingHigh.ReasoningOption())
	}
}
