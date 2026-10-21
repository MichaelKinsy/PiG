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
	// OutputPad is the horizontal padding configured by the outputPad setting. Renderers with renderShell "self" apply it themselves.
	OutputPad int `json:"outputPad"`
	// DurationMs is the recorded execution time of a final result in milliseconds. Nil while the result is partial and for results
	// stored without one. upstream: types.ts:496 ToolRenderContext.durationMs
	DurationMs *int64 `json:"durationMs,omitempty"`
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

// ToolRenderers is how calls to a tool are drawn: the renderShell, renderCall,
// and renderResult of a [ToolDefinition].
// upstream: types.ts ToolRenderers
type ToolRenderers struct {
	RenderShell  ToolRenderShell
	RenderCall   ToolRenderCallFunc
	RenderResult ToolRenderResultFunc
}

// ToolRendererResolver chooses how calls to a tool are drawn, including tools
// that are not registered. next returns the renderers the remaining
// resolvers, then the registered tool, would use. A nil result means none.
// upstream: types.ts ToolRendererResolver
type ToolRendererResolver = func(toolName string, next func() *ToolRenderers) *ToolRenderers

// ToolPrepareArgumentsFunc mirrors upstream ToolDefinition.prepareArguments.
type ToolPrepareArgumentsFunc = func(args json.RawMessage) (json.RawMessage, error)

// ToolExposure mirrors upstream ToolExposure: how the model reaches a tool.
// "Callable" means callable from other tools through `ctx.executeTool()`, as
// the codemode tool does.
//
//   - direct: declared to the model while active, and callable while active.
//   - model-only: declared to the model while active, never callable.
//   - codemode: callable whenever registered. Not declared to the model unless
//     explicitly activated. Codemode tools list it in their description.
//   - deferred: like codemode, but codemode tools do not list it; tool search
//     can find it.
//   - hidden: registered but unreachable. Activating it has no effect.
//
// `direct` and `model-only` tools are activated when they are registered; the
// others are not.
//
// upstream: types.ts:509 (ToolExposure)
type ToolExposure string

// The exposures of upstream's ToolExposure union.
const (
	ToolExposureDirect    ToolExposure = "direct"
	ToolExposureModelOnly ToolExposure = "model-only"
	ToolExposureCodemode  ToolExposure = "codemode"
	ToolExposureDeferred  ToolExposure = "deferred"
	ToolExposureHidden    ToolExposure = "hidden"
)

// ToolAnnotations mirrors upstream ToolAnnotations: hints about what a tool
// does, with the meaning of MCP tool annotations. They come from the tool's
// author and are not verified.
//
// upstream: types.ts:515 (ToolAnnotations)
type ToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

// ToolNamespace mirrors upstream ToolNamespace: a group of related tools, such
// as the tools of one MCP server.
//
// upstream: types.ts:527 (ToolNamespace)
type ToolNamespace struct {
	Name string `json:"name"`
	// Aliases are other names that refer to the namespace where tools look it up by name (codemode's describeNamespace()
	// and the namespace option of searchTools()), for example `docs` for `mcp__docs`. A nil slice is an absent member;
	// an explicit empty one encodes as [], as upstream returns the authored namespace object (agent-session.ts:1505).
	Aliases []string `json:"aliases,omitzero"`
	// Description is a short summary shown once with the group in model-facing tool listings.
	Description string `json:"description,omitempty"`
	// Instructions is longer usage guidance, such as MCP server instructions. It is not part of tool listings; tools
	// that describe the namespace on request (codemode's describeNamespace()) return it.
	Instructions string `json:"instructions,omitempty"`
}

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
	// DefaultActive is whether registering the tool activates it. Default: true
	// for direct and model-only tools; other exposures are never activated on
	// registration. A tool with DefaultActive false is activated by naming it in
	// `--tools` or the `defaultTools` setting, or with SetActiveTools.
	// upstream: types.ts:600 (defaultActive)
	DefaultActive *bool `json:"defaultActive,omitempty"`
	// PrepareLoadout adjusts how the loadout is presented to the model while
	// this tool is active. Called whenever the active tools change. Tools that
	// orchestrate other tools use it, for example to list the callable tools in
	// their own description.
	// upstream: types.ts:607 (prepareLoadout)
	PrepareLoadout ToolPrepareLoadoutFunc `json:"-"`
	// ReserveCallOrder reserves this call's place among the calls the tool's requests must follow in call order. The host
	// calls it synchronously, in call order, before any call of a parallel batch or a codemode script runs, and puts the
	// result on the call's context (CallOrderFromContext). A nil result reserves nothing. Pi's calls run their
	// synchronous prefix in call order, which Go goroutines do not give; this restores it for tools whose request order is
	// observable, such as MCP tool calls to one server.
	ReserveCallOrder func(params json.RawMessage) *CallOrder `json:"-"`
	// MayTakeFileMutationQueue is set for a tool whose code runs in an extension that can register in the host's file mutation queue (withFileMutationQueue),
	// so the dispatcher holds the edit and write calls that follow it in a parallel batch behind the end of its synchronous prefix (agent.MayTakeFileMutationQueue).
	MayTakeFileMutationQueue bool `json:"-"`
	// ReachesTools is for tools that reach other tools (such as codemode, which calls them, and tool_search, which loads
	// them): which tools a call with the given input may reach. Extensions that register tools late, such as MCP servers
	// that are still connecting, use it from their tool_call handler to finish registering those tools before the call runs.
	// upstream: types.ts:621 (reachesTools)
	ReachesTools ToolReachesToolsFunc `json:"-"`
	// BuiltInRenderers names the built-in tool whose renderers draw the card
	// halves this definition does not render itself. A subprocess extension
	// sets it for a tool built from Pi's create<Tool>ToolDefinition, whose
	// renderers are that built-in tool's (D73).
	BuiltInRenderers string `json:"-"`
	// ValidationParameters is the host-only representation of non-enumerable TypeBox metadata.
	ValidationParameters json.RawMessage `json:"-"`
	// OutputSchema is the schema of the structured result. upstream: types.ts:585
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	// Exposure is how the model reaches the tool. Default: direct. upstream: types.ts:590
	Exposure ToolExposure `json:"exposure,omitempty"`
	// Namespace groups the tool with related tools. upstream: types.ts:593
	Namespace *ToolNamespace `json:"namespace,omitempty"`
	// Annotations are hints about what the tool does. upstream: types.ts:596
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolReach mirrors the result of upstream ToolDefinition.reachesTools, `"all" | readonly string[]`: every tool, or the
// words of the input that may name them, where a word refers to a tool or namespace whose name contains it, so words
// may over-match.
//
// pig Go mechanic (not a divergence): a Go struct stands for the string-or-array union.
type ToolReach struct {
	All   bool
	Words []string
}

// ToolReachesToolsFunc mirrors upstream ToolDefinition.reachesTools: which tools a call with input (the call's
// arguments as the tool receives them) may reach.
type ToolReachesToolsFunc = func(input json.RawMessage) ToolReach

// ToolInfo mirrors upstream ToolInfo: the read-only view returned by
// [API.GetAllTools].
type ToolInfo struct {
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Parameters       json.RawMessage `json:"parameters"`
	PromptGuidelines []string        `json:"promptGuidelines,omitempty"`
	SourceInfo       SourceInfo      `json:"sourceInfo"`
	// Source is PiG's per-tool source attribution (D23): "builtin", the registering extension's name, or the tool's declared source. It is not part of upstream's ToolInfo.
	Source string `json:"-"`
	// Exposure, Namespace, and Annotations mirror upstream ToolInfo. upstream: types.ts:2063
	Exposure    ToolExposure     `json:"exposure,omitempty"`
	Namespace   *ToolNamespace   `json:"namespace,omitempty"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
	// ReachesTools is the tool's reachesTools, nil for a tool that reaches no others. upstream: types.ts:2107 (ToolInfo)
	ReachesTools ToolReachesToolsFunc `json:"-"`
}

// RegisteredTool mirrors upstream RegisteredTool: the host's bookkeeping
// after a tool is registered.
type RegisteredTool struct {
	Definition ToolDefinition `json:"definition"`
	SourceInfo SourceInfo     `json:"sourceInfo"`
	// Source is PiG's per-tool source attribution (D23): the tool's declared source, or the registering extension's name. It is not part of upstream's RegisteredTool and is empty for tools no extension registered.
	Source string `json:"-"`
	// Revision identifies this registration: a live registry stamps every registration, including one that replaces a name, with a value no other registration of the process has, so a consumer that derived something from a definition can tell that the definition is the one it saw. Zero is a registration no live registry stamped, which identifies nothing.
	Revision uint64 `json:"-"`
}

// CallOrder is a reserved place in a tool's call order. Pi runs each call's synchronous prefix in call order, so
// `Promise.all([tools.a(), tools.b()])` issues `a` first; Go's goroutines give no such order, so the host reserves each call's
// place in call order (ToolDefinition.ReserveCallOrder) and a call issues its request only after every earlier reservation
// released. Wait returns when every earlier reservation was released; Release, which is idempotent, hands the place on and must
// run however the call ends.
type CallOrder struct {
	Wait    func()
	Release func()
}

type callOrderKey struct{}

// WithCallOrder returns ctx carrying the reservation of the call it is passed into.
func WithCallOrder(ctx context.Context, order *CallOrder) context.Context {
	return context.WithValue(ctx, callOrderKey{}, order)
}

// CallOrderFromContext returns the reservation the host made for this call, if any.
func CallOrderFromContext(ctx context.Context) (*CallOrder, bool) {
	order, ok := ctx.Value(callOrderKey{}).(*CallOrder)
	return order, ok && order != nil
}

type dispatchStartedKey struct{}

// WithDispatchStarted returns ctx carrying started, which an event dispatch made with it calls once its first handler has started: an in-process handler when it returns, an extension process's handler when its request is written to the process. Pi starts the tool_result hooks of a parallel batch in the order their tools settled, each running to its first await before the next starts; the caller holds the hooks of later calls back until started runs. started must be idempotent.
func WithDispatchStarted(ctx context.Context, started func()) context.Context {
	return context.WithValue(ctx, dispatchStartedKey{}, started)
}

// DispatchStartedFromContext returns the function the dispatch calls when its first handler has started, or nil.
func DispatchStartedFromContext(ctx context.Context) func() {
	started, _ := ctx.Value(dispatchStartedKey{}).(func())
	return started
}
