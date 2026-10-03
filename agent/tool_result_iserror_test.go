package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 0.99.1 keeps two error flags apart. `event.isError` and `outcome.isError` say the call failed. `result.isError`
// exists only when the tool RETURNED a failure (types.ts:436-441; bash.ts:403-409; codemode/execute.ts:318;
// mcp/tools.ts:224). A thrown error, an unknown tool, invalid arguments, a blocked call and a throwing hook produce
// createErrorToolResult, `{content, details: {}}` with no isError (agent-loop.ts:906-910, 748-752, 856-861, 902-905), and
// afterToolCall's `isError` replaces only the outer flag (agent-loop.ts:890). The RPC and JSON wire writes the result
// object as it is (agent-loop.ts:912-919), so the distinction is observable.

func toolEndEvents(events []AgentEvent) []ToolExecutionEndEvent {
	var ends []ToolExecutionEndEvent
	for _, event := range events {
		if end, ok := event.(ToolExecutionEndEvent); ok {
			ends = append(ends, end)
		}
	}
	return ends
}

func iserrorTool(name string, execute func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error)) *scriptTool {
	return &scriptTool{name: name, params: valueSchema, execute: execute}
}

func failedResult() AgentToolResult {
	return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "bad"}}, Details: map[string]any{"partial": true}, IsError: true}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestAgentLoop_ToolExecutionEndKeepsResultIsErrorApartFromTheEventFlag(t *testing.T) {
	returnsFailure := func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		return failedResult(), nil
	}
	returnsSuccess := func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: map[string]any{}}, nil
	}
	throws := func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		return AgentToolResult{}, errors.New("thrown")
	}
	panics := func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		panic("panicked")
	}
	yes, no := true, false
	override := func(isError *bool) []AfterToolCallHook {
		return []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
			return AfterToolCallResult{IsError: isError}
		}}
	}
	thrownResult := func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "marked"}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
	}
	echoArgs := ai.JsonObject{"value": "x"}
	cases := []struct {
		name      string
		call      ai.ToolCall
		tool      *scriptTool
		before    []BeforeToolCallHook
		after     []AfterToolCallHook
		eventErr  bool
		resultErr bool
		text      string
		details   any
	}{
		// agent-loop.ts:840 (isError: result.isError === true) keeps the flag on the returned result.
		{name: "returned failure", call: toolCall("c1", "t", echoArgs), tool: iserrorTool("t", returnsFailure), eventErr: true, resultErr: true, text: "bad", details: map[string]any{"partial": true}},
		{name: "returned success", call: toolCall("c1", "t", echoArgs), tool: iserrorTool("t", returnsSuccess), text: "ok", details: map[string]any{}},
		// agent-loop.ts:841-847: a thrown error becomes createErrorToolResult, which has no isError.
		{name: "thrown error", call: toolCall("c1", "t", echoArgs), tool: iserrorTool("t", throws), eventErr: true, text: "thrown", details: map[string]any{}},
		{name: "panic", call: toolCall("c1", "t", echoArgs), tool: iserrorTool("t", panics), eventErr: true, text: "panicked", details: map[string]any{}},
		// A built-in that represents a throw as a marked result reports the call as failed and drops the result's own isError.
		{name: "result marked Thrown", call: toolCall("c1", "t", echoArgs), tool: iserrorTool("t", thrownResult), eventErr: true, text: "marked", details: map[string]any{}},
		// agent-loop.ts:748-752.
		{name: "unknown tool", call: toolCall("c1", "missing", echoArgs), tool: iserrorTool("t", returnsSuccess), eventErr: true, text: "Tool missing not found", details: map[string]any{}},
		// agent-loop.ts:756-758 (validateToolArguments throws inside prepareToolCall's try); the required `value` is missing.
		{name: "invalid arguments", call: toolCall("c1", "t", ai.JsonObject{}), tool: iserrorTool("t", returnsSuccess), eventErr: true, text: `Validation failed for tool "t":`, details: map[string]any{}},
		// agent-loop.ts:742-752.
		{name: "blocked call", call: toolCall("c1", "t", echoArgs), tool: iserrorTool("t", returnsSuccess), eventErr: true, text: "nope", details: map[string]any{},
			before: []BeforeToolCallHook{func(context.Context, string, string, json.RawMessage) ToolCallHookResult {
				return ToolCallHookResult{Block: true, Reason: "nope"}
			}}},
		// agent-loop.ts:890: `isError = afterResult.isError ?? isError` changes only the outer flag; the {...result} spread keeps the tool's.
		{name: "hook sets isError on a success", call: toolCall("c1", "t", echoArgs), tool: iserrorTool("t", returnsSuccess), after: override(&yes), eventErr: true, text: "ok", details: map[string]any{}},
		{name: "hook clears isError of a returned failure", call: toolCall("c1", "t", echoArgs), tool: iserrorTool("t", returnsFailure), after: override(&no), resultErr: true, text: "bad", details: map[string]any{"partial": true}},
		// agent-loop.ts:902-905: a throwing hook replaces the result with createErrorToolResult.
		{name: "throwing hook", call: toolCall("c1", "t", echoArgs), tool: iserrorTool("t", returnsFailure), eventErr: true, text: "hook failed", details: map[string]any{},
			after: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
				panic("hook failed")
			}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &scriptedProvider{respond: toolCallsThenText(tc.call)}
			rec := newEventRecorder(nil)
			a := NewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{tc.tool}, EventCh: rec.ch, BeforeToolCall: tc.before, AfterToolCall: tc.after})

			msgs := mustSend(t, a, "go")

			ends := toolEndEvents(rec.stop())
			if len(ends) != 1 {
				t.Fatalf("tool_execution_end events = %d, want 1", len(ends))
			}
			end := ends[0]
			if end.IsError != tc.eventErr {
				t.Errorf("event.IsError = %v, want %v", end.IsError, tc.eventErr)
			}
			if end.Result.IsError != tc.resultErr {
				t.Errorf("event.Result.IsError = %v, want %v", end.Result.IsError, tc.resultErr)
			}
			if !strings.Contains(end.Result.Text(), tc.text) {
				t.Errorf("result text = %q, want it to contain %q", end.Result.Text(), tc.text)
			}
			if got, want := mustJSON(t, end.Result.Details), mustJSON(t, tc.details); got != want {
				t.Errorf("result details = %s, want %s", got, want)
			}
			var persisted *ToolResultMessage
			for _, msg := range msgs {
				if msg.ToolResult != nil {
					persisted = msg.ToolResult
				}
			}
			if persisted == nil || persisted.IsError != tc.eventErr {
				t.Errorf("persisted toolResult message = %+v, want isError %v (agent-loop.ts:930)", persisted, tc.eventErr)
			}
		})
	}
}

// A blocked call keeps `terminate` on the result (agent-loop.ts:746-748) and never carries isError.
func TestAgentLoop_BlockedCallResultCarriesTerminateWithoutIsError(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c1", "t", ai.JsonObject{"value": "x"}))}
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider), Tools: []AgentTool{iserrorTool("t", nil)}, EventCh: rec.ch,
		BeforeToolCall: []BeforeToolCallHook{func(context.Context, string, string, json.RawMessage) ToolCallHookResult {
			return ToolCallHookResult{Block: true, Reason: "stop", Terminate: true}
		}},
	})

	mustSend(t, a, "go")

	ends := toolEndEvents(rec.stop())
	if len(ends) != 1 || !ends[0].IsError || ends[0].Result.IsError || !ends[0].Result.Terminate {
		t.Fatalf("tool_execution_end = %+v, want event.isError, no result.isError, result.terminate", ends)
	}
}

// afterToolCall's result is spread over the tool's (agent-loop.ts:877-889): every key keeps its position, and assigning an existing
// key keeps it, so only a structuredContent the hook adds to a result that had none follows the tool's keys. Pi 0.99.1 (probed, bash
// failing exit with a tool_result handler returning details) writes content, details, structuredContent, isError.
func TestAgentLoop_ToolExecutionEndRecordsAStructuredContentTheHookAppended(t *testing.T) {
	added := json.RawMessage(`{"added":true}`)
	plain := func() AgentTool { return valueEchoTool(ToolModeParallel, nil) }
	cases := []struct {
		name string
		tool AgentTool
		hook AfterToolCallResult
		want bool
	}{
		{"hook adds structuredContent to a result without one", plain(), AfterToolCallResult{StructuredContent: added}, true},
		{"hook replaces the tool's structuredContent", newStructuredEchoTool(), AfterToolCallResult{StructuredContent: added}, false},
		{"hook keeps the tool's structuredContent", newStructuredEchoTool(), AfterToolCallResult{Details: "kept"}, false},
		{"content-only hook drops it", newStructuredEchoTool(), AfterToolCallResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}}, false},
		{"hook without structuredContent on a result without one", plain(), AfterToolCallResult{Details: "kept"}, false},
		{"empty override", plain(), AfterToolCallResult{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c1", "echo", ai.JsonObject{"value": "x"}))}
			rec := newEventRecorder(nil)
			a := NewAgent(AgentOptions{
				Model: scriptedModel(provider), Tools: []AgentTool{tc.tool}, EventCh: rec.ch,
				AfterToolCall: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
					return tc.hook
				}},
			})

			mustSend(t, a, "go")

			ends := toolEndEvents(rec.stop())
			if len(ends) != 1 || ends[0].Result.StructuredContentAppended != tc.want {
				t.Fatalf("tool_execution_end = %+v, want StructuredContentAppended %v", ends, tc.want)
			}
		})
	}
	t.Run("no hook", func(t *testing.T) {
		provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c1", "echo", ai.JsonObject{"value": "x"}))}
		rec := newEventRecorder(nil)
		a := NewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{newStructuredEchoTool()}, EventCh: rec.ch})
		mustSend(t, a, "go")
		if ends := toolEndEvents(rec.stop()); len(ends) != 1 || ends[0].Result.StructuredContentAppended {
			t.Fatalf("tool_execution_end = %+v, want no StructuredContentAppended", ends)
		}
	})
}

// runToolCall resolves with `{toolCall, result, isError}` where result is the object the tool returned or
// createErrorToolResult (agent-loop.ts:800-820); nested-tool-calls.ts:244 forwards both flags.
func TestRunToolCall_OutcomeKeepsResultIsErrorApartFromOutcomeFlag(t *testing.T) {
	throws := func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		return AgentToolResult{}, errors.New("thrown")
	}
	returnsFailure := func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		return failedResult(), nil
	}
	for _, tc := range []struct {
		name      string
		call      ai.ToolCall
		execute   func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error)
		resultErr bool
	}{
		{"returned failure", toolCall("a", "t", ai.JsonObject{"value": "x"}), returnsFailure, true},
		{"thrown error", toolCall("b", "t", ai.JsonObject{"value": "x"}), throws, false},
		{"unknown tool", toolCall("c", "missing", ai.JsonObject{"value": "x"}), returnsFailure, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome, err := RunToolCall(context.Background(), tc.call, RunToolCallOptions{Tools: []AgentTool{iserrorTool("t", tc.execute)}})
			if err != nil {
				t.Fatal(err)
			}
			if !outcome.IsError || outcome.Result.IsError != tc.resultErr {
				t.Fatalf("outcome = %+v, want isError true and result.isError %v", outcome, tc.resultErr)
			}
		})
	}
}
