package extension

import (
	"context"
	"encoding/json"
)

// ToolRenderResultOptions mirrors upstream ToolRenderResultOptions.
type ToolRenderResultOptions struct {
	Expanded  bool `json:"expanded"`
	IsPartial bool `json:"isPartial"`
}

// ToolRenderContext mirrors upstream ToolRenderContext<TState, TArgs>.
//
// Go mechanic (not a divergence): upstream is generic over TState and TArgs; the
// Go version uses any for both because Go function-type struct fields cannot have
// type parameters. SDK helpers in `extensions/sdk/go/` provide typed wrappers
// without changing the wire format.
type ToolRenderContext struct {
	Args             any       `json:"args"`
	ToolCallID       string    `json:"toolCallId"`
	Invalidate       func()    `json:"-"`
	LastComponent    Component `json:"-"`
	State            any       `json:"-"`
	Cwd              string    `json:"cwd"`
	ExecutionStarted bool      `json:"executionStarted"`
	ArgsComplete     bool      `json:"argsComplete"`
	IsPartial        bool      `json:"isPartial"`
	Expanded         bool      `json:"expanded"`
	ShowImages       bool      `json:"showImages"`
	IsError          bool      `json:"isError"`
	// Card identifies the tool card being rendered. Go mechanic (not a
	// divergence): a renderer that runs in an extension process keeps State
	// and its last component there, so the host names the card they belong
	// to; upstream passes the card's objects themselves.
	Card string `json:"-"`
}

// ToolRenderShell controls whether the standard tool-execution chrome wraps
// the tool's renderers, or the tool draws its own framing. Mirrors upstream
// "default" | "self".
type ToolRenderShell string

const (
	ToolRenderShellDefault ToolRenderShell = "default"
	ToolRenderShellSelf    ToolRenderShell = "self"
)

// ToolExecuteFunc mirrors upstream ToolDefinition.execute.
//
// Go carries the upstream abort signal and extension values through one
// context.Context. Use [FromContext] to access the extension context.
type ToolExecuteFunc = func(
	ctx context.Context,
	toolCallID string,
	params json.RawMessage,
	onUpdate AgentToolUpdateCallback,
) (AgentToolResult, error)

// ToolRenderCallFunc mirrors upstream ToolDefinition.renderCall.
type ToolRenderCallFunc = func(
	args json.RawMessage,
	theme Theme,
	context ToolRenderContext,
) Component

// ToolRenderResultFunc mirrors upstream ToolDefinition.renderResult.
type ToolRenderResultFunc = func(
	result AgentToolResult,
	options ToolRenderResultOptions,
	theme Theme,
	context ToolRenderContext,
) Component

// ToolPrepareArgumentsFunc mirrors upstream ToolDefinition.prepareArguments.
type ToolPrepareArgumentsFunc = func(args json.RawMessage) (json.RawMessage, error)

// ToolDefinition mirrors upstream ToolDefinition<TParams, TDetails, TState>.
//
// pig Go mechanic (not a divergence): upstream is generic over TParams (TypeBox
// TSchema), TDetails, and TState. Go uses json.RawMessage for parameters and any
// for details/state because Go interface methods cannot have type parameters and
// the host registry is heterogeneous. The SDK ergonomics layer in
// `extensions/sdk/go/` provides typed wrappers without changing the wire format.
type ToolDefinition struct {
	Name                string                   `json:"name"`
	Label               string                   `json:"label"`
	Description         string                   `json:"description"`
	PromptSnippet       string                   `json:"promptSnippet,omitempty"`
	PromptGuidelines    []string                 `json:"promptGuidelines,omitempty"`
	Parameters          json.RawMessage          `json:"parameters"`
	ConstrainedSampling json.RawMessage          `json:"constrainedSampling,omitempty"`
	RenderShell         ToolRenderShell          `json:"renderShell,omitempty"`
	PrepareArguments    ToolPrepareArgumentsFunc `json:"-"`
	ExecutionMode       ToolExecutionMode        `json:"executionMode,omitempty"`
	Execute             ToolExecuteFunc          `json:"-"`
	RenderCall          ToolRenderCallFunc       `json:"-"`
	RenderResult        ToolRenderResultFunc     `json:"-"`
}

// ToolInfo mirrors upstream ToolInfo: the read-only view returned by
// [API.GetAllTools].
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	SourceInfo  SourceInfo      `json:"sourceInfo"`
}

// RegisteredTool mirrors upstream RegisteredTool: the host's bookkeeping
// after a tool is registered.
type RegisteredTool struct {
	Definition ToolDefinition `json:"definition"`
	SourceInfo SourceInfo     `json:"sourceInfo"`
}
