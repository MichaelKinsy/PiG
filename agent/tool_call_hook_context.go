package agent

import (
	"context"
)

// ToolCallHookContext is what Pi hands a tool-call hook beside the call itself: the assistant message that issued the call and the agent context at that point.
//
// A before or after hook receives it through its context.Context ([ToolCallHookContextFrom]); the loop and [RunToolCall] attach it before the hooks run.
//
// upstream: packages/agent/src/types.ts BeforeToolCallContext / AfterToolCallContext (assistantMessage, context)
type ToolCallHookContext struct {
	// AssistantMessage issued the tool call. Nil for a RunToolCall made without one.
	AssistantMessage *AssistantMessage
	// Context is the agent context (messages and tools) the call runs in. Its Messages are the run's own array, cut at its length, the
	// same one for every call of the assistant message, as in Pi: what a hook or a tool replaces in place changes the run, and the
	// other calls and the next request see it; an append goes to an array of the caller's own. The run does not touch the array while
	// the calls run, and no two calls' hooks write it at the same time: a sequential batch runs one call at a time, the before hooks
	// of a parallel batch run one after another, and its after hooks follow the order the tools settled. The tools of a parallel batch
	// do run at the same time: code that reads or writes the messages while another call's tool or hook may do so synchronizes with it.
	Context AgentContext
}

type toolCallHookContextKey struct{}

// WithToolCallHookContext returns ctx carrying the hook context.
func WithToolCallHookContext(ctx context.Context, hookContext ToolCallHookContext) context.Context {
	return context.WithValue(ctx, toolCallHookContextKey{}, hookContext)
}

// ToolCallHookContextFrom returns the hook context a tool-call hook runs with.
func ToolCallHookContextFrom(ctx context.Context) (ToolCallHookContext, bool) {
	hookContext, ok := ctx.Value(toolCallHookContextKey{}).(ToolCallHookContext)
	return hookContext, ok
}

// toolCallHookContext is the loop's state for the hooks of an assistant message's calls, with messages as the context's messages.
func (r *loopRun) toolCallHookContext(assistant *AssistantMessage, messages []AgentMessage) ToolCallHookContext {
	return ToolCallHookContext{AssistantMessage: assistant, Context: AgentContext{Messages: messages, Tools: r.h.tools()}}
}
