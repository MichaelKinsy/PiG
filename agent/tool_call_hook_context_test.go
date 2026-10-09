package agent

import (
	"context"
	"encoding/json"
	"testing"
)

// upstream: agent-loop.ts runToolCall: the options' assistantMessage and context become the context argument of the before and after hooks.
func TestRunToolCallGivesHooksTheAssistantMessageAndContext(t *testing.T) {
	assistant := &AssistantMessage{Role: RoleAssistant}
	agentContext := AgentContext{Messages: []AgentMessage{userAgentMessage("hi")}, Tools: []AgentTool{noopTool()}}
	var before, after ToolCallHookContext
	var sawBefore, sawAfter bool
	options := RunToolCallOptions{
		Tools: []AgentTool{noopTool()}, AssistantMessage: assistant, Context: agentContext,
		ToolCallHooks: ToolCallHooks{
			BeforeToolCallHooks: []BeforeToolCallHook{func(ctx context.Context, _, _ string, _ json.RawMessage) ToolCallHookResult {
				before, sawBefore = ToolCallHookContextFrom(ctx)
				return ToolCallHookResult{}
			}},
			AfterToolCallHooks: []AfterToolCallHook{func(ctx context.Context, _, _ string, _ json.RawMessage, _ AgentToolResult) AfterToolCallResult {
				after, sawAfter = ToolCallHookContextFrom(ctx)
				return AfterToolCallResult{}
			}},
		},
	}
	if _, err := RunToolCall(t.Context(), toolCall("call-1", "noop", nil), options); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]ToolCallHookContext{"before": before, "after": after} {
		if got.AssistantMessage != assistant || len(got.Context.Messages) != 1 || len(got.Context.Tools) != 1 {
			t.Errorf("%s hook context = %+v", name, got)
		}
	}
	if !sawBefore || !sawAfter {
		t.Fatalf("hooks ran: before=%t after=%t", sawBefore, sawAfter)
	}
}

// A call made without them (a tool calling another tool inside a hooked call) keeps the context its outer call carries.
func TestRunToolCallKeepsTheInheritedHookContext(t *testing.T) {
	outer := ToolCallHookContext{AssistantMessage: &AssistantMessage{Role: RoleAssistant}, Context: AgentContext{Messages: []AgentMessage{userAgentMessage("outer")}}}
	var seen ToolCallHookContext
	options := RunToolCallOptions{Tools: []AgentTool{noopTool()}, ToolCallHooks: ToolCallHooks{BeforeToolCallHooks: []BeforeToolCallHook{func(ctx context.Context, _, _ string, _ json.RawMessage) ToolCallHookResult {
		seen, _ = ToolCallHookContextFrom(ctx)
		return ToolCallHookResult{}
	}}}}
	if _, err := RunToolCall(WithToolCallHookContext(t.Context(), outer), toolCall("call-1", "noop", nil), options); err != nil {
		t.Fatal(err)
	}
	if seen.AssistantMessage != outer.AssistantMessage {
		t.Fatalf("hook context = %+v, want the inherited one", seen)
	}
	var none ToolCallHookContext
	options.BeforeToolCallHooks = []BeforeToolCallHook{func(ctx context.Context, _, _ string, _ json.RawMessage) ToolCallHookResult {
		var ok bool
		none, ok = ToolCallHookContextFrom(ctx)
		if !ok {
			t.Error("hooks always get a context, empty when nothing was supplied")
		}
		return ToolCallHookResult{}
	}}
	if _, err := RunToolCall(t.Context(), toolCall("call-2", "noop", nil), options); err != nil || none.AssistantMessage != nil {
		t.Fatalf("err=%v context=%+v", err, none)
	}
}

// The loop attaches the issuing assistant message and the context, including that message and the tool list, before a model-issued call's hooks run.
func TestLoopGivesHooksTheIssuingAssistantMessage(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("call-1", "noop", nil))}
	var seen ToolCallHookContext
	var ok bool
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider), Tools: []AgentTool{noopTool()},
		BeforeToolCallHooks: []BeforeToolCallHook{func(ctx context.Context, _, _ string, _ json.RawMessage) ToolCallHookResult {
			seen, ok = ToolCallHookContextFrom(ctx)
			return ToolCallHookResult{}
		}},
	})
	if _, err := a.Send(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	if !ok || seen.AssistantMessage == nil || len(seen.AssistantMessage.Content) == 0 {
		t.Fatalf("hook context = %+v", seen)
	}
	last := seen.Context.Messages[len(seen.Context.Messages)-1]
	if last.Assistant == nil || last.Assistant != seen.AssistantMessage && last.Assistant.Timestamp != seen.AssistantMessage.Timestamp || len(seen.Context.Tools) != 1 {
		t.Fatalf("context must end with the issuing message and carry the tools: %+v", seen.Context)
	}
}
