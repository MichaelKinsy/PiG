package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/test/agent-loop.test.ts:418 "beforeToolCall receives the tool call, the validated args and the context", and
// types.ts BeforeToolCallContext {assistantMessage, toolCall, args, context}: the single beforeToolCall function gets Pi's context object.
func TestBeforeToolCallFunc_ReceivesPiContext(t *testing.T) {
	var got BeforeToolCallContext
	calls := 0
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(string) {})},
		BeforeToolCall: func(_ context.Context, call BeforeToolCallContext) *BeforeToolCallResult {
			calls++
			got = call
			return nil
		},
	})
	mustSend(t, a, "echo something")

	if calls != 1 || got.ToolCall.ID != "tool-1" || got.ToolCall.Name != "echo" {
		t.Fatalf("calls %d, tool call %+v", calls, got.ToolCall)
	}
	// types.ts:110-111: toolCall is the raw block from assistantMessage.content, so it carries the model's arguments.
	if got.ToolCall.Arguments["value"] != "hello" {
		t.Fatalf("toolCall.arguments = %v, want the raw block's {value: hello}", got.ToolCall.Arguments)
	}
	var args map[string]string
	if err := json.Unmarshal(got.Args, &args); err != nil || args["value"] != "hello" {
		t.Fatalf("args = %s (%v), want the validated value", got.Args, err)
	}
	if got.AssistantMessage == nil || len(got.Context.Messages) == 0 || len(got.Context.Tools) != 1 {
		t.Fatalf("context carries no assistant message or agent context: %+v", got)
	}
}

// upstream: agent-loop.test.ts:1783 "should stop after a blocked tool call when beforeToolCall sets terminate=true".
func TestBeforeToolCallFunc_BlockWithTerminateStopsTheRun(t *testing.T) {
	executed := false
	tool := &scriptTool{name: "echo", params: valueSchema, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		executed = true
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "should not execute"}}}, nil
	}}
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{tool},
		BeforeToolCall: func(context.Context, BeforeToolCallContext) *BeforeToolCallResult {
			return &BeforeToolCallResult{Block: true, Reason: "Blocked by policy", Terminate: true}
		},
	})
	msgs := mustSend(t, a, "echo something")
	result := findToolResult(t, msgs, "tool-1")
	if executed || provider.calls() != 1 || !result.IsError || result.Text() != "Blocked by policy" {
		t.Fatalf("executed %v, provider %d, result %+v", executed, provider.calls(), result)
	}
}

// upstream: agent-loop.test.ts:312 (afterToolCall sees the tool's result and patches usage) and 1964 "should allow afterToolCall to mark a tool batch as terminating".
func TestAfterToolCallFunc_SeesTheResultAndOverridesIt(t *testing.T) {
	toolUsage := &ai.Usage{Input: 1, Output: 2, TotalTokens: 3}
	patchedUsage := &ai.Usage{Input: 5, Output: 6, TotalTokens: 11}
	tool := valueEchoTool(ToolModeParallel, func(string) {})
	base := tool.execute
	tool.execute = func(ctx context.Context, id string, args json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
		result, err := base(ctx, id, args, onUpdate)
		result.Usage = toolUsage
		return result, err
	}
	terminate := true
	var seen AfterToolCallContext
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{tool},
		AfterToolCall: func(_ context.Context, call AfterToolCallContext) *AfterToolCallResult {
			seen = call
			return &AfterToolCallResult{Usage: patchedUsage, Terminate: &terminate}
		},
	})
	msgs := mustSend(t, a, "echo something")

	if seen.Result.Usage != toolUsage || seen.IsError || seen.ToolCall.ID != "tool-1" || seen.ToolCall.Arguments["value"] != "hello" || seen.AssistantMessage == nil || len(seen.Context.Messages) == 0 {
		t.Fatalf("afterToolCall context = %+v", seen)
	}
	if got := findToolResult(t, msgs, "tool-1").Usage; got == nil || *got != *patchedUsage {
		t.Fatalf("tool result usage = %+v, want %+v", got, patchedUsage)
	}
	if provider.calls() != 1 {
		t.Fatalf("provider calls = %d, want the terminating batch to end the run", provider.calls())
	}
}

// Pi's property is assignable: agent.beforeToolCall = fn (agent.ts:201-207, read when a run starts). The Go hook lists run after it.
func TestAgentBeforeToolCallProperty_IsAssignableAndRunsBeforeTheHookList(t *testing.T) {
	var order []string
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(string) {})},
		BeforeToolCallHooks: []BeforeToolCallHook{func(context.Context, string, string, json.RawMessage) ToolCallHookResult {
			order = append(order, "list")
			return ToolCallHookResult{}
		}},
	})
	a.BeforeToolCall = func(context.Context, BeforeToolCallContext) *BeforeToolCallResult {
		order = append(order, "property")
		return nil
	}
	mustSend(t, a, "echo something")
	if want := []string{"property", "list"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("hook order = %v, want %v", order, want)
	}
}
