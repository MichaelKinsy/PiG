// extension_bridge.go adapts tools from coding/extension onto the
// agent.AgentTool interface used by the agent loop.
//
// `coding/extension/tool.go` defines `RegisteredTool` and
// `ToolDefinition` to match upstream pi's wire shape (TypeBox
// `json.RawMessage` parameters; Execute returns the agent's
// AgentToolResult). `agent.AgentTool` is pig's interface used by
// every callsite in the agent loop. This bridge owns the schema
// marshalling required to span the two.
//
// The adapter holds a value copy of `extension.RegisteredTool`. It
// does NOT hold a reference to the runner: staleness is the
// runner's concern.
//
// Ports packages/coding-agent/src/core/extensions/wrapper.ts.
// Ports packages/coding-agent/src/core/tools/tool-definition-wrapper.ts.

package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// bridgeTool adapts a single `extension.RegisteredTool` onto the
// `agent.AgentTool` interface so it can join the per-session tool
// slice. Parameters is unmarshalled once from `json.RawMessage` to
// `map[string]any` so `Schema()` is cheap (called per agent turn).
type bridgeTool struct {
	def extension.ToolDefinition
	// declaration holds the parameters the extension wrote, decoded by ai.ToolSchema so their member order survives.
	declaration         ai.ToolSchema
	constrainedSampling *ai.ConstrainedSamplingConfig
	// constrainedSamplingDisabled is an explicit `constrainedSampling: false`, kept for transcript declarations.
	constrainedSamplingDisabled bool
	// runner is the extension runner whose tool context each call gets (wrapper.ts wrapRegisteredTool); nil leaves the context to the caller.
	runner *inproc.Runner
}

// newBridgeTool constructs a bridgeTool, returning an error if the
// embedded JSON Schema is malformed.
func newBridgeTool(rt extension.RegisteredTool) (*bridgeTool, error) {
	var declaration ai.ToolSchema
	if len(rt.Definition.Parameters) > 0 {
		envelope, err := json.Marshal(struct {
			Parameters json.RawMessage `json:"parameters"`
		}{rt.Definition.Parameters})
		if err == nil {
			err = json.Unmarshal(envelope, &declaration)
		}
		if err != nil {
			return nil, fmt.Errorf("bridge tool %q: parse parameters: %w", rt.Definition.Name, err)
		}
	}
	// Constrained sampling is tri-state on the wire: absent, the JSON literal
	// false, or a config object. False behaves like absent for providers but
	// stays visible in transcript declarations (transcript.ts:123-129).
	sampling, err := parseConstrainedSampling(rt.Definition.ConstrainedSampling)
	if err != nil {
		return nil, fmt.Errorf("bridge tool %q: parse constrained_sampling: %w", rt.Definition.Name, err)
	}
	disabled := sampling == nil && strings.TrimSpace(string(rt.Definition.ConstrainedSampling)) == "false"
	return &bridgeTool{def: rt.Definition, declaration: declaration, constrainedSampling: sampling, constrainedSamplingDisabled: disabled}, nil
}

// parseConstrainedSampling decodes a tool's constrained sampling request. An
// absent, null, or false value disables it (returns nil), matching upstream's
// `false | ConstrainedSamplingConfig` where false is equivalent to undefined.
func parseConstrainedSampling(raw json.RawMessage) (*ai.ConstrainedSamplingConfig, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "false" || trimmed == "null" {
		return nil, nil
	}
	var cfg ai.ConstrainedSamplingConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Name returns the tool's name as known to the LLM.
func (b *bridgeTool) Name() string { return b.def.Name }

// Label returns the human-readable display label (or empty for default).
func (b *bridgeTool) Label() string { return b.def.Label }

// Schema returns the JSON Schema for the tool's parameters in the
// shape expected by `ai.ToolSchema`.
func (b *bridgeTool) Schema() ai.ToolSchema {
	schema := b.declaration
	schema.Name = b.def.Name
	schema.Description = b.def.Description
	schema.PromptGuidelines = b.def.PromptGuidelines
	schema.ConstrainedSampling = b.constrainedSampling
	schema.ConstrainedSamplingDisabled = b.constrainedSamplingDisabled
	return schema
}

// OutputSchema returns the JSON Schema of the structured content in the tool's successful results.
//
// upstream: tool-definition-wrapper.ts:17,52 (outputSchema)
func (b *bridgeTool) OutputSchema() json.RawMessage { return b.def.OutputSchema }

// ArgumentSchema keeps TypeBox's non-enumerable conversion metadata off provider requests.
func (b *bridgeTool) ArgumentSchema() json.RawMessage {
	if len(b.def.ValidationParameters) > 0 {
		return b.def.ValidationParameters
	}
	if len(b.def.Parameters) > 0 {
		return b.def.Parameters
	}
	return json.RawMessage(`{}`)
}

func (b *bridgeTool) PrepareArguments(params json.RawMessage) (json.RawMessage, error) {
	if b.def.PrepareArguments == nil {
		return params, nil
	}
	return b.def.PrepareArguments(params)
}

// Execute invokes the underlying `extension.ToolDefinition.Execute` (types.ts ToolDefinition.execute): the result is an
// [agent.AgentToolResult] and onUpdate the [agent.ToolUpdateCallback] the agent loop passes.
func (b *bridgeTool) Execute(
	ctx context.Context,
	toolCallID string,
	params json.RawMessage,
	onUpdate agent.ToolUpdateCallback,
) (agent.AgentToolResult, error) {
	if b.def.Execute == nil {
		return agent.AgentToolResult{}, fmt.Errorf("bridge tool %q: Execute is nil", b.def.Name)
	}

	if ticket, ok := agent.MutationTicketFromContext(ctx); ok && b.def.ReserveCallOrder != nil {
		order := &extension.CallOrder{Wait: ticket.Wait, Release: ticket.Release}
		ctx = extension.WithCallOrder(ctx, order)
		defer order.Release()
	}
	if b.runner != nil {
		ctx = b.runner.ToolCallContext(ctx, toolCallID)
	}
	raw, err := b.def.Execute(ctx, toolCallID, params, onUpdate)
	if err != nil {
		return agent.AgentToolResult{}, err
	}

	return raw, nil
}

// ReserveMutationOrder reserves the call's place in the order the definition keeps (ToolDefinition.ReserveCallOrder).
func (b *bridgeTool) ReserveMutationOrder(params json.RawMessage) (*agent.MutationTicket, bool) {
	if b.def.ReserveCallOrder == nil {
		return nil, false
	}
	order := b.def.ReserveCallOrder(params)
	if order == nil {
		return nil, false
	}
	return &agent.MutationTicket{Wait: order.Wait, Release: order.Release}, true
}

// ExecutionMode returns the tool's parallelism preference. Only an explicit sequential definition serializes execution.
func (b *bridgeTool) ExecutionMode() agent.ToolExecutionMode {
	if b.def.ExecutionMode == "sequential" {
		return agent.ToolModeSequential
	}
	return agent.ToolModeParallel
}

// WrapRegisteredTool wraps a registered tool into an AgentTool whose every call gets runner's tool context for its call id, as wrapper.ts wrapRegisteredTool
// does through wrapToolDefinition. A schema that fails to parse is an error.
func WrapRegisteredTool(registeredTool extension.RegisteredTool, runner *inproc.Runner) (agent.AgentTool, error) {
	tool, err := newBridgeTool(registeredTool)
	if err != nil {
		return nil, err
	}
	tool.runner = runner
	return tool, nil
}

// WrapRegisteredTools wraps every registered tool in order (wrapper.ts wrapRegisteredTools); the first tool that cannot be wrapped fails the call.
func WrapRegisteredTools(registeredTools []extension.RegisteredTool, runner *inproc.Runner) ([]agent.AgentTool, error) {
	tools := make([]agent.AgentTool, 0, len(registeredTools))
	for _, registeredTool := range registeredTools {
		tool, err := WrapRegisteredTool(registeredTool, runner)
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// BridgeNewRunnerTools converts extension-registered tools into AgentTools
// suitable for the agent loop. Preserves registration order. Tools whose
// schema fails to parse are skipped with a returned diagnostics slice.
func BridgeNewRunnerTools(rts []extension.RegisteredTool) ([]agent.AgentTool, []error) {
	if len(rts) == 0 {
		return nil, nil
	}
	out := make([]agent.AgentTool, 0, len(rts))
	var diags []error
	for _, rt := range rts {
		bt, err := newBridgeTool(rt)
		if err != nil {
			diags = append(diags, err)
			continue
		}
		out = append(out, bt)
	}
	return out, diags
}
