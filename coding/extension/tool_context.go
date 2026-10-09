package extension

// Ports packages/coding-agent/src/core/extensions/types.ts (ExecuteToolOptions, ExtensionToolContext).
// Ports packages/coding-agent/src/core/extensions/runner.ts (createToolContext).

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// AgentToolCall mirrors pi-agent-core AgentToolCall: the tool call block a tool call runs for.
type AgentToolCall = ai.ToolCall

// AgentToolCallOutcome mirrors pi-agent-core AgentToolCallOutcome: the final outcome of a tool call after the hooks ran.
//
// upstream: .upstream/v0.99.1/packages/agent/src/types.ts (AgentToolCallOutcome)
type AgentToolCallOutcome struct {
	ToolCall AgentToolCall   `json:"toolCall"`
	Result   AgentToolResult `json:"result"`
	IsError  bool            `json:"isError"`
	// DurationMs is the milliseconds execute() took, measured with a monotonic clock; absent when the tool did not run. upstream: agent/src/types.ts:454 AgentToolCallOutcome.durationMs
	DurationMs *int64 `json:"durationMs,omitempty"`
}

// AgentTool is the read-only view of a tool that a tool call sees through [ToolContext.Tools] and [ToolLoadout].
// Go mechanic (not a divergence): upstream's AgentTool carries `execute`. A tool runs only through [ToolContext.ExecuteTool], so the view holds the declaration fields and no function.
//
// upstream: .upstream/v0.99.1/packages/agent/src/types.ts (AgentTool)
type AgentTool struct {
	Name                string            `json:"name"`
	Label               string            `json:"label"`
	Description         string            `json:"description"`
	Parameters          json.RawMessage   `json:"parameters"`
	OutputSchema        json.RawMessage   `json:"outputSchema,omitempty"`
	ConstrainedSampling json.RawMessage   `json:"constrainedSampling,omitempty"`
	ExecutionMode       ToolExecutionMode `json:"executionMode,omitempty"`
}

// ExecuteToolOptions mirrors upstream ExecuteToolOptions.
//
// upstream: types.ts:367-372 (ExecuteToolOptions)
type ExecuteToolOptions struct {
	// Signal cancels the nested call. Defaults to the calling tool's context. Go mechanic (not a divergence): upstream's AbortSignal is a context.Context, as in [ExecOptions].
	Signal context.Context `json:"-"`
	// OnUpdate receives partial results of the nested tool, in addition to `tool_execution_update` events.
	OnUpdate agent.ToolUpdateSink `json:"-"`
}

// ToolActions is the host-side injection that backs [ToolContext]. Mirrors the executeTool and getCallableTools members of upstream ExtensionContextActions.
//
// upstream: types.ts:2161-2169 (ExtensionContextActions.executeTool, getCallableTools)
type ToolActions struct {
	// ExecuteTool backs [ToolContext.ExecuteTool]. ctx is the options' Signal, or the calling tool's context; options.Signal is nil. Without it, nested calls fail with an error outcome.
	// upstream: types.ts:2162
	ExecuteTool func(ctx context.Context, callerID, name string, args json.RawMessage, options ExecuteToolOptions) (AgentToolCallOutcome, error)
	// GetCallableTools backs [ToolContext.Tools]. Without it, the list is empty.
	// upstream: types.ts:2169
	GetCallableTools func() []AgentTool
	// AppendEntry backs [ToolContext.AppendEntry]: append the entry to the session's log and report it as an `entry_appended` event. Go returns the error the log reports, where upstream throws it.
	// upstream: agent-session.ts:3321-3327 (ExtensionActions.appendEntry, types.ts:1687)
	AppendEntry func(customType string, data any) error
}

// ToolContext is the context passed to tool Execute in a session: the extension [Context] plus [ToolContext.ExecuteTool] for running other tools through the same validation, hooks, and permission checks as model-issued calls. Retrieve it with [ToolContextFromContext].
//
// A tool that no runner wrapped, such as a built-in tool run in a plain Agent or called directly, gets no ToolContext.
//
// upstream: types.ts:383-395 (ExtensionToolContext)
type ToolContext struct {
	*Context
	toolCallID string
	signal     context.Context
	actions    ToolActions
}

// NewToolContext builds the context of the tool call toolCallID. signal is the default cancellation of nested calls. Hosts call it; tool authors never do.
//
// upstream: runner.ts:952-985 (createToolContext)
func NewToolContext(base *Context, toolCallID string, signal context.Context, actions ToolActions) *ToolContext {
	return &ToolContext{Context: base, toolCallID: toolCallID, signal: signal, actions: actions}
}

// Tools returns the tools [ToolContext.ExecuteTool] can call. Without a GetCallableTools action the list is empty.
//
// upstream: types.ts:385 (ExtensionToolContext.tools), runner.ts:955-964, 379
func (c *ToolContext) Tools() ([]AgentTool, error) {
	if err := c.assertActive(); err != nil {
		return nil, err
	}
	if c.actions.GetCallableTools == nil {
		return []AgentTool{}, nil
	}
	return c.actions.GetCallableTools(), nil
}

// AppendEntry appends a custom entry to the session for state persistence (it is not sent to the LLM) and reports it as an `entry_appended` event. It is `pi.appendEntry()`, for a declarative Go tool that has no `pi` to close over: upstream's codemode passes `(customType, data) => pi.appendEntry(customType, data)` into its tool definition. Without a bound action the call fails with [ErrRuntimeNotInitialized].
//
// upstream: loader.ts:376-379 (appendEntry: assertActive, then runtime.appendEntry), loader.ts:157-159, 171 (notInitialized), agent-session.ts:3321-3327, codemode/index.ts:35
func (c *ToolContext) AppendEntry(customType string, data any) error {
	if err := c.assertActive(); err != nil {
		return err
	}
	if c.actions.AppendEntry == nil {
		return ErrRuntimeNotInitialized
	}
	return c.actions.AppendEntry(customType, data)
}

// ExecuteTool runs another tool. The call gets the id `<calling id>/<n>`, and the `tool_call`, `tool_result`, and `tool_execution_*` events carry `parentToolCallId`. It does not appear in the transcript; a bounded record of it is kept as `nestedCalls` on the calling tool's result message.
//
// It never fails for tool failures: unknown tools, validation errors, blocked calls, and thrown errors come back as an outcome with IsError set. The error is non-nil only for a stale context or unencodable arguments. The call is cancelled with options.Signal, which defaults to the calling tool's context.
//
// upstream: types.ts:386-394 (executeTool), runner.ts:966-983
func (c *ToolContext) ExecuteTool(name string, args any, options *ExecuteToolOptions) (AgentToolCallOutcome, error) {
	if err := c.assertActive(); err != nil {
		return AgentToolCallOutcome{}, err
	}
	if c.actions.ExecuteTool == nil {
		return AgentToolCallOutcome{}, errNestedCallsUnavailable
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return AgentToolCallOutcome{}, err
	}
	// runner.ts:981: `{ ...options, signal: options.signal ?? signal }`.
	ctx := c.signal
	var opts ExecuteToolOptions
	if options != nil {
		opts = *options
		if opts.Signal != nil {
			ctx = WithOwnSignal(opts.Signal)
		}
	}
	opts.Signal = nil
	return c.actions.ExecuteTool(ctx, c.toolCallID, name, raw, opts)
}

// errNestedCallsUnavailable is returned by a ToolContext built without an ExecuteTool action. A [Runner]-built context supplies upstream's error outcome instead.
var errNestedCallsUnavailable = errors.New("Nested tool calls are not available in this context")

type toolCtxKey struct{}

// WithToolContext attaches a ToolContext to a context.Context. Hosts call it; authors do not.
func WithToolContext(parent context.Context, tc *ToolContext) context.Context {
	return context.WithValue(parent, toolCtxKey{}, tc)
}

// ToolContextFromContext returns the ToolContext a host attached to a tool call, or nil when the tool runs outside a runner.
func ToolContextFromContext(ctx context.Context) *ToolContext {
	tc, _ := ctx.Value(toolCtxKey{}).(*ToolContext)
	return tc
}

// WithOwnSignal marks ctx as the context of a nested call that runs with the signal its caller passed in options.signal.
//
// upstream: runner.ts:979-981 (`{ ...options, signal: options.signal ?? signal }`)
func WithOwnSignal(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownSignalKey{}, true)
}

// OwnsSignal reports whether ctx belongs to a nested call that runs with the signal its caller passed in options.signal. Other tool calls run with the run's signal.
func OwnsSignal(ctx context.Context) bool {
	own, _ := ctx.Value(ownSignalKey{}).(bool)
	return own
}

type ownSignalKey struct{}
