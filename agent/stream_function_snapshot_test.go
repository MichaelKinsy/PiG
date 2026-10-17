package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/agent/src/agent.ts:436-455 runPromptMessages and runContinuation pass `this.streamFunction` to runAgentLoop once, when the run starts. A streamFunction assigned while that run is in flight (here from inside a tool) is not seen by the run's later requests; the next run uses it.
func TestStreamFunctionAssignedDuringARunAppliesToTheNextRun(t *testing.T) {
	var calls []string
	var a *Agent
	custom := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		calls = append(calls, "custom")
		return doneStream(textMessage("custom")), nil
	}
	requests := 0
	stock := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		requests++
		calls = append(calls, "stock")
		if requests == 1 {
			return doneStream(toolUseMessage(toolCall("t1", "swap", nil))), nil
		}
		return doneStream(textMessage("done")), nil
	}
	swap := &scriptTool{name: "swap", params: map[string]any{"type": "object"}, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		a.SetStreamFunction(custom)
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "swapped"}}}, nil
	}}
	a = mustNewAgent(AgentOptions{Tools: []AgentTool{swap}, DefaultStreamFn: stock})

	mustSend(t, a, "first")
	if want := []string{"stock", "stock"}; !slices.Equal(calls, want) {
		t.Fatalf("first run requests = %v, want %v: the stream function is fixed when the run starts", calls, want)
	}
	mustSend(t, a, "second")
	if want := []string{"stock", "stock", "custom"}; !slices.Equal(calls, want) {
		t.Fatalf("after the run = %v, want the assigned function on the next run: %v", calls, want)
	}
}
