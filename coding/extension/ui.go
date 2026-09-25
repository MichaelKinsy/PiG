package extension

import (
	"context"
	"errors"
)

// ErrUIUnavailable reports that a UI-only operation cannot run in the current mode.
var ErrUIUnavailable = errors.New("UI not available")

// UIContext is the per-mode UI surface for extensions.
//
// **Upstream alias:** mirrors `ExtensionUIContext` (types.ts:120-269).
// Each mode (interactive, RPC, print) provides its own implementation;
// extensions interact with whichever the host bound at runtime.
//
// **Default:** when the host binds nothing, `Context.UI()` returns
// the package-level [NoopUIContext] (a value, not a constructor -
// matches upstream's module-level `noOpUIContext` constant at
// runner.ts:188).
//
// **Method shape conventions (pig):**
//
//   - Methods that upstream returns `Promise<T | undefined>` from
//     map to `(T, error)` in Go. The error half carries cancellation
//     and routes upstream "undefined" through the typed zero value
//     (empty string, false). Callers distinguish "user cancelled"
//     from "ok with default value" by inspecting `errors.Is(err,
//     context.Canceled)`.
//   - Methods that upstream returns `Promise<void>` from map to
//     `error`-returning Go methods so cancellation propagates.
//   - Methods that upstream returns synchronously stay synchronous
//     in Go (Notify, SetStatus, etc.).
//   - Method names are upstream camelCase → Go PascalCase. The
//     `setX` family becomes `SetX`. The readonly property `theme`
//     becomes a `Theme()` getter (Go has no readonly fields on
//     interfaces).
//
// **Sync-compat:** every divergence from a 1:1 method shape is a
// silent sync-debt risk. Method order in this interface mirrors
// upstream types.ts order verbatim so `git diff` against the next
// upstream sync is mechanical.
//
// upstream: types.ts:120-269 (ExtensionUIContext)
type UIContext interface {
	// Show a selector and return the user's choice.
	// upstream: types.ts:124
	Select(ctx context.Context, title string, options []string, opts ExtensionUIDialogOptions) (string, error)

	// Show a confirmation dialog.
	// upstream: types.ts:127
	Confirm(ctx context.Context, title, message string, opts ExtensionUIDialogOptions) (bool, error)

	// Show a text input dialog.
	// upstream: types.ts:130
	Input(ctx context.Context, title, placeholder string, opts ExtensionUIDialogOptions) (string, error)

	// Show a notification to the user. Type is one of
	// "info" | "warning" | "error".
	// upstream: types.ts:133
	Notify(message, kind string)

	// OnTerminalInput registers a raw-terminal-input handler
	// (interactive mode only). Returns an unsubscribe function.
	// upstream: types.ts:136
	OnTerminalInput(handler TerminalInputHandler) (unsubscribe func())

	// SetStatus sets status text in the footer/status bar. Empty
	// text clears the entry.
	// upstream: types.ts:139
	SetStatus(key, text string)

	// SetWorkingMessage sets the working/loading message shown
	// during streaming. Empty message restores the default.
	// upstream: types.ts:142
	SetWorkingMessage(message string)

	// SetWorkingVisible toggles whether the working/loading indicator is shown.
	// upstream: interactive-mode.ts oauth/rpc extension contexts
	SetWorkingVisible(visible bool)

	// SetWorkingIndicator configures the interactive working
	// indicator. Nil opts restores the default animated spinner.
	// upstream: types.ts:153
	SetWorkingIndicator(opts WorkingIndicatorOptions)

	// SetHiddenThinkingLabel sets the label shown for hidden
	// thinking blocks. Empty label restores the default.
	// upstream: types.ts:156
	SetHiddenThinkingLabel(label string)

	// SetWidget sets a widget to display above or below the editor.
	// content may be []string or a function-typed factory; opts
	// describes placement/lifecycle.
	//
	// upstream: types.ts:159 + types.ts:163 (overloaded). Go merges
	// to one signature using `any` for content because Go has no
	// method overloading.
	SetWidget(key string, content any, opts ExtensionWidgetOptions)

	// SetFooter installs a custom footer factory. Nil restores the
	// built-in footer.
	// upstream: types.ts:172
	SetFooter(factory any)

	// SetHeader installs a custom header factory. Nil restores the
	// built-in header.
	// upstream: types.ts:181
	SetHeader(factory any)

	// pig additive (D60): SetLogin validates and installs Pig's native login
	// definition in the shared header slot. Upstream Pi exposes only
	// SetHeader because its in-process extensions can supply component factories.
	SetLogin(definition LoginDefinition) error

	// SetTitle sets the terminal window/tab title.
	// upstream: types.ts:184
	SetTitle(title string)

	// Custom shows a custom component with keyboard focus and
	// returns the user's result via the `done` callback.
	//
	// upstream: types.ts:187: the generic `<T>` is erased to `any` because Go
	// interface methods cannot be generic (an ordinary TS→Go mechanic, not a
	// divergence). Authors typed-assert at the call site or use the SDK helpers.
	Custom(ctx context.Context, factory any, opts any) (any, error)

	// PasteToEditor pastes text into the editor, triggering paste
	// handling (collapse for large content).
	// upstream: types.ts:204
	PasteToEditor(text string)

	// SetEditorText sets the text in the core input editor.
	// upstream: types.ts:207
	SetEditorText(text string)

	// GetEditorText returns the current text from the core input
	// editor.
	// upstream: types.ts:210
	GetEditorText() string

	// Editor shows a multi-line editor for text editing. Returns
	// the entered text or empty + context.Canceled when cancelled.
	// upstream: types.ts:213
	Editor(ctx context.Context, title, prefill string) (string, error)

	// AddAutocompleteProvider stacks additional autocomplete
	// behavior on top of the built-in provider.
	// upstream: types.ts:216
	AddAutocompleteProvider(factory AutocompleteProviderFactory)

	// SetEditorComponent installs a custom editor component
	// factory. Nil restores the default editor.
	// upstream: types.ts:249
	SetEditorComponent(factory any)

	// GetEditorComponent returns the currently installed custom editor component.
	// Nil when the default editor is active.
	GetEditorComponent() any

	// Theme returns the current theme for styling.
	//
	// upstream: types.ts:254 (`readonly theme: Theme`). Mapped to a
	// getter method because Go interfaces cannot expose readonly
	// fields.
	Theme() Theme

	// GetAllThemes returns metadata for all available themes.
	// upstream: types.ts:257
	GetAllThemes() []ThemeMeta

	// GetTheme loads a theme by name without switching to it.
	// Returns nil + error if not found.
	// upstream: types.ts:260
	GetTheme(name string) (Theme, error)

	// SetTheme switches the current theme. theme is either a name
	// (string) or a Theme value; the result reports success +
	// optional error message.
	// upstream: types.ts:262
	SetTheme(theme any) SetThemeResult

	// GetToolsExpanded returns the current tool-output expansion
	// state.
	// upstream: types.ts:265
	GetToolsExpanded() bool

	// SetToolsExpanded sets the tool-output expansion state.
	// upstream: types.ts:268
	SetToolsExpanded(expanded bool)

	// RunRemoteOverlay opens an interactive overlay whose contents
	// are produced by a remote (subprocess) extension. The overlay
	// renders the cached lines pushed via the returned handle and
	// forwards user input chunks back to host.OnInput. Blocks until
	// the handle's Close is called and returns the result value the
	// remote producer supplied. Returns (nil, false) when the
	// overlay cannot be opened (e.g. no TUI bound, like in print or
	// RPC modes).
	//
	// pig-specific: no upstream equivalent. Upstream's Custom()
	// can't cross a subprocess boundary because its factory captures
	// in-process TUI references; this is the deserialised companion.
	RunRemoteOverlay(opts RemoteOverlayOptions, host RemoteOverlayHost, onHandle func(RemoteOverlayHandle)) (any, bool)

	// OnRemoteTerminalInput registers the raw-terminal-input listener of
	// the subprocess extension named extensionName. It takes its place in
	// registration order as OnTerminalInput does, but the host never calls
	// handler on its input loop: it asks off the loop and applies the
	// verdict in input order. Returns an unsubscribe function.
	//
	// pig additive (D19): upstream listeners run in process and answer
	// synchronously; a subprocess listener's verdict crosses a socket.
	OnRemoteTerminalInput(extensionName string, handler RemoteTerminalInputHandler) (unsubscribe func())
}

// noopUIContext is the per-package no-op UI context returned when
// nothing has been bound. Mirrors upstream's `noOpUIContext` constant
// at runner.ts:188-217.
//
// Every method returns the upstream-equivalent value for an unavailable UI:
// empty input, false confirmation, no-op mutation, or an empty collection.
type noopUIContext struct{}

// NoopUIContext is the package-level no-op singleton. Hosts that have
// no UI (RPC mode, print mode) wire `ContextActions.UI = NoopUIContext`;
// equivalently, leaving `ContextActions.UI` nil produces the same
// behavior because [Context.UI] falls back to this value.
//
// **Identity contract:** the value is intentionally a singleton (one
// shared `*noopUIContext`) so `Context.HasUI()` can implement
// upstream's identity check (runner.ts:361: `this.uiContext !==
// noOpUIContext`) by pointer comparison.
//
// upstream: runner.ts:188 (`const noOpUIContext: ExtensionUIContext = ...`)
var NoopUIContext UIContext = &noopUIContext{}

func (*noopUIContext) Select(context.Context, string, []string, ExtensionUIDialogOptions) (string, error) {
	return "", nil
}
func (*noopUIContext) Confirm(context.Context, string, string, ExtensionUIDialogOptions) (bool, error) {
	return false, nil
}
func (*noopUIContext) Input(context.Context, string, string, ExtensionUIDialogOptions) (string, error) {
	return "", nil
}
func (*noopUIContext) Notify(string, string)                                  {}
func (*noopUIContext) OnTerminalInput(TerminalInputHandler) func()            { return func() {} }
func (*noopUIContext) SetStatus(string, string)                               {}
func (*noopUIContext) SetWorkingMessage(string)                               {}
func (*noopUIContext) SetWorkingVisible(bool)                                 {}
func (*noopUIContext) SetWorkingIndicator(WorkingIndicatorOptions)            {}
func (*noopUIContext) SetHiddenThinkingLabel(string)                          {}
func (*noopUIContext) SetWidget(string, any, ExtensionWidgetOptions)          {}
func (*noopUIContext) SetFooter(any)                                          {}
func (*noopUIContext) SetHeader(any)                                          {}
func (*noopUIContext) SetLogin(LoginDefinition) error                         { return ErrUIUnavailable }
func (*noopUIContext) SetTitle(string)                                        {}
func (*noopUIContext) Custom(context.Context, any, any) (any, error)          { return nil, nil }
func (*noopUIContext) PasteToEditor(string)                                   {}
func (*noopUIContext) SetEditorText(string)                                   {}
func (*noopUIContext) GetEditorText() string                                  { return "" }
func (*noopUIContext) Editor(context.Context, string, string) (string, error) { return "", nil }
func (*noopUIContext) AddAutocompleteProvider(AutocompleteProviderFactory)    {}
func (*noopUIContext) SetEditorComponent(any)                                 {}
func (*noopUIContext) GetEditorComponent() any                                { return nil }
func (*noopUIContext) Theme() Theme                                           { return nil }
func (*noopUIContext) GetAllThemes() []ThemeMeta                              { return nil }
func (*noopUIContext) GetTheme(string) (Theme, error)                         { return nil, nil }
func (*noopUIContext) SetTheme(any) SetThemeResult {
	// Mirrors the unavailable-UI result at runner.ts:214: `{ success: false,
	// error: "UI not available" }`. The string is upstream-verbatim
	// so an extension that pattern-matches on the error message
	// works identically against pig's noop.
	return SetThemeResult{Success: false, Error: "UI not available"}
}
func (*noopUIContext) GetToolsExpanded() bool { return false }
func (*noopUIContext) SetToolsExpanded(bool)  {}
func (*noopUIContext) RunRemoteOverlay(RemoteOverlayOptions, RemoteOverlayHost, func(RemoteOverlayHandle)) (any, bool) {
	return nil, false
}
func (*noopUIContext) OnRemoteTerminalInput(string, RemoteTerminalInputHandler) func() {
	return func() {}
}

// WidthLines is a component frame rendered outside the host (by a subprocess
// extension) together with the terminal width it was rendered at, so the host
// never paints a frame rendered for another width. Width 0 means unknown.
type WidthLines struct {
	Lines []string
	Width int
}
