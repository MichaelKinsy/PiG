package inproc

// Ports packages/coding-agent/src/core/extensions/runner.ts (createToolContext).
// Ports packages/coding-agent/src/core/extensions/wrapper.ts and core/tools/tool-definition-wrapper.ts (the per-call context factory).

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// CreateToolContext returns the [extension.ToolContext] of the tool call toolCallID: the extension context plus the callable tools and executeTool. ctx is the default cancellation of nested calls (upstream's `signal`).
//
// upstream: runner.ts:958-985 (createToolContext(toolCallId, signal): ExtensionToolContext)
func (r *Runner) CreateToolContext(ctx context.Context, toolCallID string) *extension.ToolContext {
	ctx = r.dispatchContext(ctx)
	base := extension.FromContext(ctx)
	actions := extension.ToolActions{
		ExecuteTool:      r.contextActions.ExecuteTool,
		GetCallableTools: r.contextActions.GetCallableTools,
		AppendEntry:      r.contextActions.AppendEntry,
	}
	if actions.ExecuteTool == nil {
		actions.ExecuteTool = unavailableNestedCall
	}
	return extension.NewToolContext(base, toolCallID, ctx, actions)
}

// ToolCallContext returns ctx with the extension [extension.Context] and the tool call's [extension.ToolContext] attached. Tool wrappers call it once per call, as upstream's wrapToolDefinition calls its context factory with the call id and signal.
//
// upstream: wrapper.ts:14-19, tool-definition-wrapper.ts:7-30
func (r *Runner) ToolCallContext(ctx context.Context, toolCallID string) context.Context {
	ctx = r.dispatchContext(ctx)
	return extension.WithToolContext(ctx, r.CreateToolContext(ctx, toolCallID))
}

// unavailableNestedCall is upstream's outcome when the runner has no executeTool action: an error whose call id is `<caller>/0`.
//
// upstream: runner.ts:968-976
func unavailableNestedCall(_ context.Context, callerID, name string, _ json.RawMessage, _ extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
	return extension.AgentToolCallOutcome{
		ToolCall: ai.ToolCall{ID: callerID + "/0", Name: name, Arguments: ai.JsonObject{}},
		Result: agent.AgentToolResult{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Nested tool calls are not available in this context"}},
			Details: map[string]any{},
		},
		IsError: true,
	}, nil
}
