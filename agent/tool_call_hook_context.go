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
	// Context is the agent context (messages and tools) the call runs in. Its Messages are the run's own array, cut at its length, for a
	// hook that runs while no other call's hook or tool does (a sequential batch, a batch of one call, and the before hooks of a
	// parallel batch, which run one after another as in Pi): a hook that replaces a message in place changes the run, and the next
	// hook and the next request see it, as in Pi; an append goes to an array of the hook's own. The tool and the after hooks of two or
	// more calls executed in parallel receive a copy instead, because they run at the same time: what they write there stays theirs.
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
