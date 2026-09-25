// extension_bridge.go adapts tools from coding/extension onto the
// agent.AgentTool interface used by the agent loop.
//
// `coding/extension/tool.go` defines `RegisteredTool` and
// `ToolDefinition` to match upstream pi's wire shape (TypeBox
// `json.RawMessage` parameters, generic-erased Execute returning
// `any` per D1). `agent.AgentTool` is pig's interface used by
// every callsite in the agent loop. This bridge owns the type
// assertions and schema marshalling required to span the two.
//
// The adapter holds a value copy of `extension.RegisteredTool`. It
// does NOT hold a reference to the runner: staleness is the
// runner's concern.
//
// pig-specific: no upstream equivalent (upstream uses TypeBox-typed
// generic tools all the way down).

package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// bridgeTool adapts a single `extension.RegisteredTool` onto the
// `agent.AgentTool` interface so it can join the per-session tool
// slice. Parameters is unmarshalled once from `json.RawMessage` to
// `map[string]any` so `Schema()` is cheap (called per agent turn).
type bridgeTool struct {
	def                 extension.ToolDefinition
	schema              map[string]any
	constrainedSampling *ai.ConstrainedSamplingConfig
}

// newBridgeTool constructs a bridgeTool, returning an error if the
// embedded JSON Schema is malformed.
func newBridgeTool(rt extension.RegisteredTool) (*bridgeTool, error) {
	var schema map[string]any
	if len(rt.Definition.Parameters) > 0 {
		if err := json.Unmarshal(rt.Definition.Parameters, &schema); err != nil {
			return nil, fmt.Errorf("bridge tool %q: parse parameters: %w", rt.Definition.Name, err)
		}
	}
	// Constrained sampling is tri-state on the wire: absent, the JSON literal
	// false, or a config object. Upstream treats false as equivalent to
	// undefined (disabled), so both collapse to a nil config here.
	sampling, err := parseConstrainedSampling(rt.Definition.ConstrainedSampling)
	if err != nil {
		return nil, fmt.Errorf("bridge tool %q: parse constrained_sampling: %w", rt.Definition.Name, err)
	}
	return &bridgeTool{def: rt.Definition, schema: schema, constrainedSampling: sampling}, nil
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
	return ai.ToolSchema{
		Name:                b.def.Name,
		Description:         b.def.Description,
		Parameters:          b.schema,
		PromptGuidelines:    b.def.PromptGuidelines,
		ConstrainedSampling: b.constrainedSampling,
	}
}

func (b *bridgeTool) PrepareArguments(params json.RawMessage) (json.RawMessage, error) {
	if b.def.PrepareArguments == nil {
		return params, nil
	}
	return b.def.PrepareArguments(params)
}

// Execute invokes the underlying `extension.ToolDefinition.Execute`
// and type-asserts the result back to `agent.AgentToolResult`.
//
// **Type-assertion contract.** D1 erases the result type to `any`
// at the interface boundary. Registered tools must return
// `agent.AgentToolResult`. A different shape returns an error.
//
// `onUpdate` is forwarded as `any` because
// `extension.AgentToolUpdateCallback` is an `any` alias. Tools that stream
// progress type-assert it to `agent.ToolUpdateCallback`.
func (b *bridgeTool) Execute(
	ctx context.Context,
	toolCallID string,
	params json.RawMessage,
	onUpdate agent.ToolUpdateCallback,
) (agent.AgentToolResult, error) {
	if b.def.Execute == nil {
		return agent.AgentToolResult{}, fmt.Errorf("bridge tool %q: Execute is nil", b.def.Name)
	}

	raw, err := b.def.Execute(ctx, toolCallID, params, onUpdate)
	if err != nil {
		return agent.AgentToolResult{}, err
	}

	// D1: extension.AgentToolResult is `= any`. Tools written for
	// pig return agent.AgentToolResult. A nil return is allowed
	// (legacy builtins occasionally use this idiom alongside an
	// in-band success Content); fall through to the zero value.
	if raw == nil {
		return agent.AgentToolResult{}, nil
	}
	res, ok := raw.(agent.AgentToolResult)
	if !ok {
		return agent.AgentToolResult{}, fmt.Errorf("bridge tool %q: Execute returned %T, want agent.AgentToolResult", b.def.Name, raw)
	}
	return res, nil
}

// ExecutionMode returns the tool's parallelism preference. Defaults
// to sequential when the definition leaves it empty.
func (b *bridgeTool) ExecutionMode() agent.ToolExecutionMode {
	switch b.def.ExecutionMode {
	case "parallel":
		return agent.ToolModeParallel
	default:
		// Empty or any unrecognized value falls back to sequential.
		// This matches both upstream's default and the legacy adapter.
		return agent.ToolModeSequential
	}
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
