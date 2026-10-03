package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A rejected update sink ends runToolCall's promise chain, so the call rejects with the first sink error: executePreparedToolCall collects every update's promise and awaits them after the tool returns, in the try body and again in its catch, so the rethrow leaves executePreparedToolCall and skips finalizeExecutedToolCall. The tool still runs to the end, and every later update still reaches the sink.
//
// upstream: .upstream/v0.99.1/packages/agent/src/agent-loop.ts:810-820 (runToolCall), 820-849 (executePreparedToolCall), 851-905 (finalizeExecutedToolCall)
func TestRunToolCall_RejectsWithTheFirstUpdateSinkErrorAndSkipsAfterToolCall(t *testing.T) {
	var mu sync.Mutex
	var order []string
	note := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, entry)
	}
	tool := &scriptTool{name: "progress", label: "Progress", description: "Reports progress", params: map[string]any{"type": "object", "properties": map[string]any{}},
		execute: func(_ context.Context, _ string, _ json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
			onUpdate(AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "one"}}, Details: map[string]any{}})
			onUpdate(AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "two"}}, Details: map[string]any{}})
			onUpdate(AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "three"}}, Details: map[string]any{}})
			note("tool finished")
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{}}, nil
		}}
	first, second := errors.New("first sink error"), errors.New("second sink error")
	options := RunToolCallOptions{
		Tools: []AgentTool{tool},
		ToolCallHooks: ToolCallHooks{AfterToolCall: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
			note("afterToolCall")
			return AfterToolCallResult{}
		}}},
		OnUpdate: func(partial AgentToolResult) error {
			content := partial.Text()
			note("update " + content)
			switch content {
			case "one":
				return first
			case "three":
				return second
			}
			return nil
		},
	}
	outcome, err := RunToolCall(context.Background(), AgentToolCall{ID: "call", Name: "progress", Arguments: ai.JsonObject{}}, options)
	if !errors.Is(err, first) {
		t.Fatalf("RunToolCall error = %v, want the first sink error", err)
	}
	if outcome.ToolCall.ID != "" || outcome.Result.Content != nil {
		t.Fatalf("a rejected call carries no outcome: %+v", outcome)
	}
	if want := []string{"update one", "update two", "update three", "tool finished"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want every update delivered, the tool finished, and no afterToolCall %v", order, want)
	}
}

// A sink that never fails leaves the call as it was: the hooks run and the outcome returns.
func TestRunToolCall_AnUpdateSinkThatSucceedsKeepsTheOutcome(t *testing.T) {
	var hooks int
	options := RunToolCallOptions{
		Tools: []AgentTool{newStructuredEchoTool()},
		ToolCallHooks: ToolCallHooks{AfterToolCall: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
			hooks++
			return AfterToolCallResult{}
		}}},
		OnUpdate: func(AgentToolResult) error { return nil },
	}
	outcome, err := RunToolCall(context.Background(), AgentToolCall{ID: "ok", Name: "echo", Arguments: ai.JsonObject{"value": "v"}}, options)
	if err != nil || outcome.ToolCall.ID != "ok" || outcome.IsError || hooks != 1 {
		t.Fatalf("outcome = %+v, error = %v, afterToolCall ran %d times", outcome, err, hooks)
	}
}
