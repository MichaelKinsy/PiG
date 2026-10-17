package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/agent/src/agent.ts:494 `transformContext: this.transformContext` copies the public property into createLoopConfig once; the loop applies that function before every request of the run. A transform assigned during a run (from a tool here) first applies to the next run.
func TestTransformContextAssignedDuringARunAppliesToTheNextRun(t *testing.T) {
	var a *Agent
	var log []string
	provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
		if call == 1 || call == 3 {
			return doneStream(toolUseMessage(toolCall("c"+string(rune('0'+call)), "swap", nil)))
		}
		return doneStream(textMessage("done"))
	}}
	swap := &scriptTool{name: "swap", params: map[string]any{"type": "object"}, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		a.SetTransformContext(func(msgs []AgentMessage) []AgentMessage { log = append(log, "new"); return msgs })
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "swapped"}}}, nil
	}}
	a = mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{swap}})
	a.SetTransformContext(func(msgs []AgentMessage) []AgentMessage { log = append(log, "old"); return msgs })

	mustSend(t, a, "one")
	if want := []string{"old", "old"}; !slices.Equal(log, want) {
		t.Fatalf("transforms of the run's two requests = %v, want %v: the run keeps the one it started with", log, want)
	}
	log = nil
	mustSend(t, a, "two")
	if want := []string{"new", "new"}; !slices.Equal(log, want) {
		t.Fatalf("next run transforms = %v, want %v", log, want)
	}
}
