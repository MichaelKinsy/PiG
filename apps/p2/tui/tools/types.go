// Package tools is how a tool call is drawn: pi's tool renderer contract (coding-agent core/extensions/types.ts `ToolRenderers`,
// `ToolRenderContext`, `ToolRendererResolver`), typed for p2: arguments and details are JSON from the conversation view, the theme is the instance
// theme (section 5), and the calls a call made to other tools come with the context. Plugins register their tools' renderers by tool name; a tool
// without any gets pi's generic card.
//
// pi: apps/p2/src/tui/tools/types.ts
package tools

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/tui"
)

// ToolArgs are a call's arguments: a JSON object in the order the model wrote it.
type ToolArgs = *jsvalue.Object

// ToolResultView is a result's content and details (a JSON value; nil when absent).
type ToolResultView struct {
	Content []ai.ToolResultMessageContent
	Details any
}

// ToolRenderResultOptions are the options of a result render.
type ToolRenderResultOptions struct{ Expanded, IsPartial bool }

// ToolRenderContext is the context of one render of one tool card.
type ToolRenderContext struct {
	// Args are the current tool call arguments. Shared across call/result renders for the same tool call.
	Args ToolArgs
	// ToolCallID is stable across call/result renders for the same tool call.
	ToolCallID string
	// Invalidate invalidates just this tool card and redraws.
	Invalidate func()
	// LastComponent is the previously returned component for this render slot, if any.
	LastComponent tui.Component
	// State is the renderer state for this card.
	State map[string]any
	// Cwd is the session's working directory, for relative path display.
	Cwd              string
	ExecutionStarted bool
	ArgsComplete     bool
	IsPartial        bool
	Expanded         bool
	// ShowImages is always false: inline images are never drawn by the worker (section 1.4).
	ShowImages bool
	IsError    bool
	// DurationMs is the final result's execution time; nil when none was recorded.
	DurationMs *float64
	OutputPad  int
	// Calls are the calls this call made to other tools (a script's), live or settled: as the tool's `nestedCalls` reads them, else as Durable shows
	// them. Nil is `undefined`.
	Calls []transcript.NestedCall
	// UI is the theme reference and key hints.
	UI render.RenderContext
}

// ToolRenderers draws one tool's calls.
type ToolRenderers struct {
	// RenderShell is "default" or "self".
	RenderShell string
	// NestedCalls reads the calls this call made to other tools from the tool's own data, when the tool keeps their final list in its result's
	// `details` (read from there once settled; `tool.Calls` holds the ones Durable shows, live or last seen). Without it, `tool.Calls`. Resolvers do not
	// change it (ResolveToolRenderers). The result is nil for `undefined`.
	NestedCalls func(tool transcript.ToolView) []transcript.NestedCall
	RenderCall  func(args ToolArgs, theme *theme.Theme, context *ToolRenderContext) tui.Component
	// RenderResult draws the result.
	RenderResult func(result ToolResultView, options ToolRenderResultOptions, theme *theme.Theme, context *ToolRenderContext) tui.Component
}

// ToolRendererResolver chooses how calls to a tool are drawn; next returns what the remaining resolvers, then the registry, would use. p2: settings are
// the visitor's; when they change, every card is drawn again with a new resolution. A resolver panics where the TypeScript throws.
type ToolRendererResolver func(toolName string, next func() *ToolRenderers, settings prefs.TuiSettings) *ToolRenderers
