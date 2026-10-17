package agent

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// BeforeToolCallContext is the context of a beforeToolCall hook (upstream types.ts BeforeToolCallContext).
type BeforeToolCallContext struct {
	// AssistantMessage is the assistant message that requested the tool call; nil for a call made without one.
	AssistantMessage *AssistantMessage
	// ToolCall is the raw tool call block from the assistant message.
	ToolCall ai.ToolCall
	// Args is the validated tool arguments.
	Args json.RawMessage
	// Context is the agent context when the tool call is prepared.
	Context AgentContext
}

// AfterToolCallContext is the context of an afterToolCall hook (upstream types.ts AfterToolCallContext).
type AfterToolCallContext struct {
	AssistantMessage *AssistantMessage
	ToolCall         ai.ToolCall
	Args             json.RawMessage
	// Result is the executed tool result before any afterToolCall override.
	Result AgentToolResult
	// IsError is whether the executed result is treated as an error.
	IsError bool
	Context AgentContext
}

// BeforeToolCallResult is what a beforeToolCall hook returns (upstream types.ts BeforeToolCallResult). It is the result type of the hook lists too.
type BeforeToolCallResult = ToolCallHookResult

// BeforeToolCall is Pi's single beforeToolCall property: it runs after argument validation and before the tool executes, in front of the Go hook list. Returning a result with Block set prevents execution; the loop then emits an error tool result. A nil result lets the call run. The context.Context is Pi's signal.
//
// upstream: packages/agent/src/types.ts AgentLoopConfig.beforeToolCall, agent-loop.ts:728-760
type BeforeToolCallFunc func(ctx context.Context, call BeforeToolCallContext) *BeforeToolCallResult

// AfterToolCallFunc is Pi's single afterToolCall property: it runs after the tool executed and before the result is finalized, in front of the Go hook list, and may override parts of the result. A nil result keeps it. The context.Context is Pi's signal.
//
// upstream: packages/agent/src/types.ts AgentLoopConfig.afterToolCall, agent-loop.ts:860-895
type AfterToolCallFunc func(ctx context.Context, call AfterToolCallContext) *AfterToolCallResult

// beforeToolCallHook adapts the single function to the hook-list form, so it runs first with Pi's context object.
func (f BeforeToolCallFunc) hook() BeforeToolCallHook {
	return func(ctx context.Context, toolCallID, toolName string, args json.RawMessage) ToolCallHookResult {
		hookContext, _ := ToolCallHookContextFrom(ctx)
		result := f(ctx, BeforeToolCallContext{
			AssistantMessage: hookContext.AssistantMessage,
			ToolCall:         rawToolCall(hookContext.AssistantMessage, toolCallID, toolName),
			Args:             args,
			Context:          hookContext.Context,
		})
		if result == nil {
			return ToolCallHookResult{}
		}
		return *result
	}
}

func (f AfterToolCallFunc) hook() AfterToolCallHook {
	return func(ctx context.Context, toolCallID, toolName string, args json.RawMessage, result AgentToolResult) AfterToolCallResult {
		hookContext, _ := ToolCallHookContextFrom(ctx)
		override := f(ctx, AfterToolCallContext{
			AssistantMessage: hookContext.AssistantMessage,
			ToolCall:         rawToolCall(hookContext.AssistantMessage, toolCallID, toolName),
			Args:             args,
			Result:           result,
			IsError:          result.IsError,
			Context:          hookContext.Context,
		})
		if override == nil {
			return AfterToolCallResult{}
		}
		return *override
	}
}

// before is the before-hook sequence of one call: Pi's single function first, then the Go list.
func (h ToolCallHooks) before() []BeforeToolCallHook {
	if h.BeforeToolCall == nil {
		return h.BeforeToolCallHooks
	}
	return append([]BeforeToolCallHook{h.BeforeToolCall.hook()}, h.BeforeToolCallHooks...)
}

// after is the after-hook sequence of one call: Pi's single function first, then the Go list.
func (h ToolCallHooks) after() []AfterToolCallHook {
	if h.AfterToolCall == nil {
		return h.AfterToolCallHooks
	}
	return append([]AfterToolCallHook{h.AfterToolCall.hook()}, h.AfterToolCallHooks...)
}

// rawToolCall is the tool call block from assistantMessage.content that Pi passes as context.toolCall (types.ts:110-111), with its raw arguments, thought signature and namespace. A call made without an assistant message, or one whose block is absent, carries only its id and name.
func rawToolCall(message *AssistantMessage, toolCallID, toolName string) ai.ToolCall {
	if message != nil {
		for _, block := range message.Content {
			if call, ok := block.(ai.ToolCall); ok && call.ID == toolCallID {
				return call
			}
		}
	}
	return ai.ToolCall{ID: toolCallID, Name: toolName}
}
