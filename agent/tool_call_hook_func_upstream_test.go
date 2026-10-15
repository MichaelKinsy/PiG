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

// Pi's single beforeToolCall/afterToolCall functions (agent.ts:123-124, 201-206; types.ts:326, 341), driven through
// AgentOptions, AgentLoopConfig and RunToolCallOptions. The cases port .upstream/current/packages/agent/test/agent-loop.test.ts
// against Pi's hook shape, plus the rejection, ordering and context contracts of agent-loop.ts.

// upstream: agent-loop.test.ts:528 "should execute mutated beforeToolCall args without revalidation". Pi's hook mutates
// the args object in place; Go's returns the replacement arguments.
func TestBeforeToolCallFuncMutatedArgsExecuteWithoutRevalidation(t *testing.T) {
	var executed []any
	tool := &scriptTool{name: "echo", params: valueSchema, execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		var decoded map[string]any
		_ = json.Unmarshal(args, &decoded)
		executed = append(executed, decoded["value"])
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "echoed"}}}, nil
	}}
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}),
		Tools: []AgentTool{tool},
		BeforeToolCall: func(context.Context, BeforeToolCallContext) *ToolCallHookResult {
			return &ToolCallHookResult{Args: json.RawMessage(`{"value":123}`)}
		},
	})

	mustSend(t, a, "echo something")

	if !reflect.DeepEqual(executed, []any{float64(123)}) {
		t.Fatalf("executed = %v, want [123]", executed)
	}
}

// upstream: agent-loop.test.ts:1841 "should continue after a mixed batch with one terminating blocked call"; the hook
// returns undefined (nil) for the call it lets through.
func TestBeforeToolCallFuncMixedBatchWithOneTerminatingBlockedCallContinues(t *testing.T) {
	var mu sync.Mutex
	var executed []string
	provider := &scriptedProvider{respond: toolCallsThenText(
		toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
		toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
	)}
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(v string) {
			mu.Lock()
			executed = append(executed, v)
			mu.Unlock()
		})},
		ToolExecution: ToolModeParallel,
		BeforeToolCall: func(_ context.Context, hookContext BeforeToolCallContext) *ToolCallHookResult {
			if argValue(hookContext.Args) == "first" {
				return &ToolCallHookResult{Block: true, Reason: "Blocked first", Terminate: true}
			}
			return nil
		},
	})

	mustSend(t, a, "echo both")

	if !reflect.DeepEqual(executed, []string{"second"}) || provider.calls() != 2 {
		t.Fatalf("executed %v, provider calls %d; want [second], 2", executed, provider.calls())
	}
}

// upstream: agent-loop.test.ts:1964 "should allow afterToolCall to mark a tool batch as terminating", through the
// standalone loop's AgentLoopConfig.afterToolCall.
func TestAgentLoopConfigAfterToolCallFuncMarksBatchTerminating(t *testing.T) {
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream {
		return doneStream(toolUseMessage(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"})))
	}}
	terminate := true
	config := standaloneConfig(provider)
	config.AfterToolCall = func(context.Context, AfterToolCallContext) *AfterToolCallResult {
		return &AfterToolCallResult{Terminate: &terminate}
	}
	agentContext := AgentContext{Tools: []AgentTool{valueEchoTool(ToolModeParallel, nil)}}
	var events []AgentEvent
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("echo something")}, agentContext, config, collectLoopEvents(&events), providerStream)
	if err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	if provider.calls() != 1 || assistantCount(messages) != 1 {
		t.Fatalf("provider calls %d, assistant turns %d; want 1, 1", provider.calls(), assistantCount(messages))
	}
}

// upstream: agent-loop.test.ts:2167 runToolCall "validates, runs the hooks, and reports failures as error outcomes",
// with the hooks reading toolCall.id and args from their context argument.
func TestRunToolCallFuncHooksValidateRunAndReportFailures(t *testing.T) {
	failing := &scriptTool{name: "failing", label: "Failing", description: "Returns an error result", params: map[string]any{"type": "object", "properties": map[string]any{}},
		execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "bad"}}, Details: map[string]any{"partial": true}, IsError: true}, nil
		}}
	assistant := &AssistantMessage{}
	var hookCalls []string
	options := RunToolCallOptions{
		Tools:            []AgentTool{newStructuredEchoTool(), failing},
		AssistantMessage: assistant,
		ToolCallHooks: ToolCallHooks{
			BeforeToolCall: func(_ context.Context, hookContext BeforeToolCallContext) *ToolCallHookResult {
				if hookContext.AssistantMessage != assistant {
					t.Errorf("before %s: assistant message %p, want %p", hookContext.ToolCall.ID, hookContext.AssistantMessage, assistant)
				}
				hookCalls = append(hookCalls, "before "+hookContext.ToolCall.ID)
				if argValue(hookContext.Args) == "blocked" {
					return &ToolCallHookResult{Block: true, Reason: "nope"}
				}
				return nil
			},
			AfterToolCall: func(_ context.Context, hookContext AfterToolCallContext) *AfterToolCallResult {
				hookCalls = append(hookCalls, "after "+hookContext.ToolCall.ID)
				return nil
			},
		},
	}
	call := func(id, name string, args ai.JsonObject) AgentToolCall {
		return AgentToolCall{ID: id, Name: name, Arguments: args}
	}

	a, _ := RunToolCall(context.Background(), call("a", "echo", ai.JsonObject{"value": "a"}), options)
	if a.ToolCall.ID != "a" || string(a.Result.StructuredContent) != `{"value":"a"}` || a.IsError {
		t.Fatalf("a = %+v", a)
	}
	if b, _ := RunToolCall(context.Background(), call("b", "echo", ai.JsonObject{"value": map[string]any{"nested": true}}), options); !b.IsError {
		t.Fatalf("b = %+v, want an error outcome", b)
	}
	c, _ := RunToolCall(context.Background(), call("c", "echo", ai.JsonObject{"value": "blocked"}), options)
	if !c.IsError || c.Result.Text() != "nope" {
		t.Fatalf("c = %+v", c)
	}
	d, _ := RunToolCall(context.Background(), call("d", "missing", ai.JsonObject{}), options)
	if !d.IsError || d.Result.Text() != "Tool missing not found" {
		t.Fatalf("d = %+v", d)
	}
	e, _ := RunToolCall(context.Background(), call("e", "failing", ai.JsonObject{}), options)
	if !e.IsError || !reflect.DeepEqual(e.Result.Details, map[string]any{"partial": true}) {
		t.Fatalf("e = %+v", e)
	}
	if want := []string{"before a", "after a", "before c", "before e", "after e"}; !reflect.DeepEqual(hookCalls, want) {
		t.Fatalf("hook calls = %v, want %v", hookCalls, want)
	}
}

// upstream: agent-loop.test.ts:2214 runToolCall "lets afterToolCall replace structured content and drops it when only
// content is replaced", plus the undefined (nil) result that keeps everything.
func TestRunToolCallAfterToolCallFuncReplacesStructuredContent(t *testing.T) {
	redacted := []ai.ToolResultMessageContent{ai.TextContent{Text: "redacted"}}
	results := []*AfterToolCallResult{
		{Content: redacted},
		{StructuredContent: json.RawMessage(`{"value":"replaced"}`)},
		{Content: redacted, StructuredContent: json.RawMessage(`{"value":"both"}`)},
		{Details: map[string]any{"note": "kept"}},
		nil,
	}
	var seen []string
	for _, afterResult := range results {
		outcome, _ := RunToolCall(context.Background(), AgentToolCall{ID: "x", Name: "echo", Arguments: ai.JsonObject{"value": "original"}}, RunToolCallOptions{
			Tools: []AgentTool{newStructuredEchoTool()},
			ToolCallHooks: ToolCallHooks{AfterToolCall: func(context.Context, AfterToolCallContext) *AfterToolCallResult {
				return afterResult
			}},
		})
		if outcome.Result.StructuredContent == nil {
			seen = append(seen, "undefined")
		} else {
			seen = append(seen, string(outcome.Result.StructuredContent))
		}
	}
	if want := []string{"undefined", `{"value":"replaced"}`, `{"value":"both"}`, `{"value":"original"}`, `{"value":"original"}`}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("structured content = %v, want %v", seen, want)
	}
}

// upstream: agent-loop.ts:770-775 turns a rejected beforeToolCall into the call's error result, and :892-895 replaces
// the result with an error result when afterToolCall rejects. A Go hook function rejects by panicking. The after
// context carries the executed result and its error flag (types.ts:119-131).
func TestToolCallFuncRejectionsBecomeErrorResults(t *testing.T) {
	failing := &scriptTool{name: "failing", params: map[string]any{"type": "object", "properties": map[string]any{}},
		execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "bad"}}, IsError: true}, nil
		}}
	tools := []AgentTool{newStructuredEchoTool(), failing}
	before, err := RunToolCall(context.Background(), AgentToolCall{ID: "x", Name: "echo", Arguments: ai.JsonObject{"value": "v"}}, RunToolCallOptions{
		Tools: tools,
		ToolCallHooks: ToolCallHooks{BeforeToolCall: func(context.Context, BeforeToolCallContext) *ToolCallHookResult {
			panic(errors.New("policy unavailable"))
		}},
	})
	if err != nil || !before.IsError || before.Result.Text() != "policy unavailable" {
		t.Fatalf("rejected before = %+v, %v", before, err)
	}
	var observed []bool
	for _, call := range []AgentToolCall{
		{ID: "ok", Name: "echo", Arguments: ai.JsonObject{"value": "v"}},
		{ID: "bad", Name: "failing", Arguments: ai.JsonObject{}},
	} {
		after, err := RunToolCall(context.Background(), call, RunToolCallOptions{
			Tools: tools,
			ToolCallHooks: ToolCallHooks{AfterToolCall: func(_ context.Context, hookContext AfterToolCallContext) *AfterToolCallResult {
				observed = append(observed, hookContext.IsError)
				panic("audit failed")
			}},
		})
		if err != nil || !after.IsError || after.Result.Text() != "audit failed" {
			t.Fatalf("%s: rejected after = %+v, %v", call.ID, after, err)
		}
	}
	if want := []bool{false, true}; !reflect.DeepEqual(observed, want) {
		t.Fatalf("afterToolCall isError = %v, want %v", observed, want)
	}
}

// The single function holds Pi's position: it runs before the Go hook list of its phase.
func TestToolCallFuncRunsBeforeTheHookList(t *testing.T) {
	var order []string
	call := AgentToolCall{ID: "x", Name: "echo", Arguments: ai.JsonObject{"value": "v"}}
	hooks := ToolCallHooks{
		BeforeToolCall: func(context.Context, BeforeToolCallContext) *ToolCallHookResult {
			order = append(order, "before func")
			return nil
		},
		BeforeToolCallHooks: []BeforeToolCallHook{func(context.Context, string, string, json.RawMessage) ToolCallHookResult {
			order = append(order, "before list")
			return ToolCallHookResult{}
		}},
		AfterToolCall: func(context.Context, AfterToolCallContext) *AfterToolCallResult {
			order = append(order, "after func")
			return nil
		},
		AfterToolCallHooks: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
			order = append(order, "after list")
			return AfterToolCallResult{}
		}},
	}
	outcome, _ := RunToolCall(context.Background(), call, RunToolCallOptions{Tools: []AgentTool{newStructuredEchoTool()}, ToolCallHooks: hooks})
	if want := []string{"before func", "before list", "after func", "after list"}; !reflect.DeepEqual(order, want) || outcome.IsError {
		t.Fatalf("order %v (want %v), outcome %+v", order, want, outcome)
	}
}

// Pi passes both single functions the raw tool call block and the agent context of the run (agent-loop.ts:730-735,
// 873-880; types.ts:107-131): the context's messages end with the assistant message that requested the call, and its
// tools are the run's tools.
func TestToolCallFuncsReceiveTheToolCallBlockAndTheAgentContext(t *testing.T) {
	tool := valueEchoTool(ToolModeParallel, nil)
	var before BeforeToolCallContext
	var after AfterToolCallContext
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}),
		Tools: []AgentTool{tool},
		BeforeToolCall: func(_ context.Context, hookContext BeforeToolCallContext) *ToolCallHookResult {
			before = hookContext
			return nil
		},
		AfterToolCall: func(_ context.Context, hookContext AfterToolCallContext) *AfterToolCallResult {
			after = hookContext
			return nil
		},
	})

	mustSend(t, a, "echo something")

	for phase, got := range map[string]struct {
		call      AgentToolCall
		assistant *AssistantMessage
		context   AgentContext
	}{
		"before": {before.ToolCall, before.AssistantMessage, before.Context},
		"after":  {after.ToolCall, after.AssistantMessage, after.Context},
	} {
		if got.call.ID != "tool-1" || got.call.Name != "echo" || !reflect.DeepEqual(got.call.Arguments, ai.JsonObject{"value": "hello"}) {
			t.Errorf("%s: toolCall = %+v, want the raw block tool-1 echo {value: hello}", phase, got.call)
		}
		if got.assistant == nil {
			t.Fatalf("%s: no assistant message", phase)
		}
		if n := len(got.context.Messages); n < 2 || got.context.Messages[n-1].Assistant == nil || !reflect.DeepEqual(*got.context.Messages[n-1].Assistant, *got.assistant) {
			t.Errorf("%s: context messages %d, want them to end with the requesting assistant message", phase, n)
		}
		if len(got.context.Tools) != 1 || got.context.Tools[0].Name() != "echo" {
			t.Errorf("%s: context tools = %v, want [echo]", phase, got.context.Tools)
		}
	}
}
