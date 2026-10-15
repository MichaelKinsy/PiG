package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/agent/src/agent.ts:201-207 beforeToolCall and afterToolCall are public properties; :480-481 createLoopConfig copies their values into the run's config once. A function assigned during a run is seen by the next run, not by the later tool calls of the one in flight; the hooks receive the validated call context (types.ts BeforeToolCallContext/AfterToolCallContext).
func TestToolCallHookPropertiesAreFixedWhenARunStarts(t *testing.T) {
	var log []string
	var a *Agent
	provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
		switch call {
		case 1:
			return doneStream(toolUseMessage(toolCall("c1", "work", ai.JsonObject{"n": 1})))
		case 2:
			return doneStream(toolUseMessage(toolCall("c2", "work", ai.JsonObject{"n": 2})))
		case 4:
			return doneStream(toolUseMessage(toolCall("c3", "work", ai.JsonObject{"n": 3})))
		}
		return doneStream(textMessage("done"))
	}}
	work := &scriptTool{name: "work", params: map[string]any{"type": "object"}, execute: func(_ context.Context, id string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		if id == "c1" {
			a.BeforeToolCall = func(_ context.Context, call BeforeToolCallContext) *BeforeToolCallResult {
				log = append(log, "before-new:"+call.ToolCall.ID)
				return nil
			}
			a.AfterToolCall = func(_ context.Context, call AfterToolCallContext) *AfterToolCallResult {
				log = append(log, "after-new:"+call.ToolCall.ID)
				return nil
			}
		}
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
	}}
	a = mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{work}})
	a.BeforeToolCall = func(_ context.Context, call BeforeToolCallContext) *BeforeToolCallResult {
		var args struct{ N int }
		_ = json.Unmarshal(call.Args, &args)
		log = append(log, "before-old:"+call.ToolCall.ID)
		if call.AssistantMessage == nil || args.N == 0 {
			t.Errorf("before hook context: assistant=%v args=%s, want the requesting message and validated arguments", call.AssistantMessage, call.Args)
		}
		return nil
	}
	a.AfterToolCall = func(_ context.Context, call AfterToolCallContext) *AfterToolCallResult {
		log = append(log, "after-old:"+call.ToolCall.ID)
		return nil
	}

	mustSend(t, a, "first")
	want := []string{"before-old:c1", "after-old:c1", "before-old:c2", "after-old:c2"}
	if !slices.Equal(log, want) {
		t.Fatalf("first run hooks = %v, want %v: the functions assigned during the run apply to the next run", log, want)
	}
	log = nil
	mustSend(t, a, "second")
	want = []string{"before-new:c3", "after-new:c3"}
	if !slices.Equal(log, want) {
		t.Fatalf("second run hooks = %v, want %v", log, want)
	}
}
