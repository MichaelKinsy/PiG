package agent

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// AgentToolCall is a tool call block of an assistant message. Mirrors upstream AgentToolCall.
type AgentToolCall = ai.ToolCall

// AgentToolCallOutcome is the final outcome of a tool call after the hooks ran. Mirrors upstream AgentToolCallOutcome (packages/agent/src/types.ts).
type AgentToolCallOutcome struct {
	ToolCall AgentToolCall
	Result   AgentToolResult
	IsError  bool
}

// OutputSchemaProvider is implemented by a tool that declares the JSON Schema of the structured content in its successful results. Mirrors upstream AgentTool.outputSchema.
type OutputSchemaProvider interface {
	OutputSchema() json.RawMessage
}

// ToolCallHooks are the hooks a tool call runs through. Mirrors upstream ToolCallHooks.
type ToolCallHooks struct {
	BeforeToolCall    []BeforeToolCallHook
	AfterToolCall     []AfterToolCallHook
	PrepareToolResult func(context.Context, AgentToolResult) AgentToolResult
}

// RunToolCallOptions configures RunToolCall. Mirrors upstream RunToolCallOptions
// (.upstream/v0.99.1/packages/agent/src/agent-loop.ts:789-798). Upstream's
// assistantMessage and context options only feed the hooks' context argument,
// which Go hook signatures do not carry, so they are not modeled; the context
// argument of RunToolCall is upstream's signal.
type RunToolCallOptions struct {
	ToolCallHooks
	// Tools the call resolves against.
	Tools []AgentTool
	// OnUpdate receives the tool's progress updates.
	OnUpdate ToolUpdateSink
}

// RunToolCall runs one tool call through the same steps as a model-issued
// call: argument preparation, schema validation, the before hooks, execution,
// and the after hooks. It emits no events and adds no messages. Tools that
// call other tools use it so the hooks (for example permission checks) apply
// to those calls too. Mirrors upstream runToolCall (agent-loop.ts:800-820).
//
// It never fails for tool failures: unknown tools, validation errors, blocked
// calls, and panics come back as an outcome with IsError set. It returns the
// first error of options.OnUpdate after the tool returned, without running the
// after hooks, as upstream's promise rejects.
func RunToolCall(ctx context.Context, call AgentToolCall, options RunToolCallOptions) (AgentToolCallOutcome, error) {
	pending := newPendingToolCall(call)
	preparation := prepareToolCall(ctx, options.Tools, options.ToolCallHooks, pending)
	if preparation.prepared == nil {
		finalized := *preparation.finalized
		return AgentToolCallOutcome{ToolCall: call, Result: finalized.result, IsError: finalized.isError}, nil
	}
	onUpdate := options.OnUpdate
	if onUpdate == nil {
		onUpdate = func(AgentToolResult) error { return nil }
	}
	executed, err := executePreparedToolCall(ctx, *preparation.prepared, onUpdate)
	if err != nil {
		return AgentToolCallOutcome{}, err
	}
	finalized := finalizeExecutedToolCall(ctx, options.ToolCallHooks, *preparation.prepared, executed)
	return AgentToolCallOutcome{ToolCall: call, Result: finalized.result, IsError: finalized.isError}, nil
}
