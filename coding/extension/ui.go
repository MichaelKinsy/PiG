package extension

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
)

// ErrUIUnavailable reports that a UI-only operation cannot run in the current mode.
var ErrUIUnavailable = errors.New("UI not available")

// WorkingMessageClearer is implemented by a [UIContext] that tells an absent working message from an empty one. Pi's setWorkingMessage(message?: string) shows
// `message ?? defaultWorkingMessage`: no argument restores the default label and an empty string is a message. ClearWorkingMessage is the no-argument form; the
// extension host calls it when an extension sends no message, and falls back to SetWorkingMessage("") for a UIContext that does not implement it. It is a separate
// interface so existing UIContext implementations keep compiling.
type WorkingMessageClearer interface {
	ClearWorkingMessage()
}

// HiddenThinkingLabelClearer is implemented by a [UIContext] that tells an absent hidden-thinking label from an empty one, as [WorkingMessageClearer] does for the working message: Pi's
// setHiddenThinkingLabel(label?: string) shows `label ?? defaultHiddenThinkingLabel` (interactive-mode.ts:2383). The extension host calls ClearHiddenThinkingLabel when an extension sends no
// label and falls back to SetHiddenThinkingLabel("") for a UIContext that does not implement it.
type HiddenThinkingLabelClearer interface {
	ClearHiddenThinkingLabel()
}

// StatusClearer is implemented by a [UIContext] that tells an absent status from an empty one. Pi's setStatus(key, text: string | undefined) writes the text
// it is given and treats `undefined` as the removal of the entry (footer-data-provider.ts setExtensionStatus); RPC mode writes `statusText` for any string,
// the empty one included, and omits it for `undefined` (rpc-mode.ts setStatus). ClearStatus is the `undefined` form. The extension host calls it when an
// extension sends no text, and falls back to SetStatus(key, "") for a UIContext that does not implement it. It is a separate interface so existing
// UIContext implementations keep compiling.
type StatusClearer interface {
	ClearStatus(key string)
}

// PlaceholderOmitter is implemented by a [UIContext] that tells an input dialog opened without a placeholder from one opened with an empty placeholder. Pi's
// input(title, placeholder?, opts?) leaves `placeholder` undefined when the extension passes none, and RPC mode omits the member then but writes
// `placeholder: ""` for an empty string (rpc-mode.ts input). InputWithoutPlaceholder is the first form. The extension host calls it when an extension sends no
// placeholder, and falls back to Input with an empty placeholder for a UIContext that does not implement it.
type PlaceholderOmitter interface {
	InputWithoutPlaceholder(ctx context.Context, title string, opts ExtensionUIDialogOptions) (string, error)
}

// PrefillOmitter is [PlaceholderOmitter] for editor(title, prefill?) (rpc-mode.ts editor): EditorWithoutPrefill opens the editor dialog with no prefill, and the
// extension host falls back to Editor with an empty prefill for a UIContext that does not implement it.
type PrefillOmitter interface {
	EditorWithoutPrefill(ctx context.Context, title string) (string, error)
}

// DialogValueUIContext is implemented by a [UIContext] whose dialogs resolve with the JSON value the user's client sent, as Pi's RPC mode does: rpc-mode.ts:137-150 and 254-266 resolve
// `"value" in r ? r.value : undefined` and `"confirmed" in r ? r.confirmed : false` without checking the type, so an extension can observe a number, `null`, an object or an array.
// The extension host answers an SDK's ui.select, ui.confirm, ui.input and ui.editor calls through these methods when the bound UI has them, and falls back to the string and bool
// methods of [UIContext] otherwise. Each returns the member's raw JSON; an undefined result (cancelled, timed out, aborted, or a response without a value) is [context.Canceled] for select, input and
// editor, and `false` for confirm. A nil placeholder or prefill is Pi's undefined: the dialog opens without that member, as [PlaceholderOmitter] and [PrefillOmitter] do for the string-typed forms.
type DialogValueUIContext interface {
	SelectValue(ctx context.Context, title string, options []string, opts ExtensionUIDialogOptions) (json.RawMessage, error)
	ConfirmValue(ctx context.Context, title, message string, opts ExtensionUIDialogOptions) (json.RawMessage, error)
	InputValue(ctx context.Context, title string, placeholder *string, opts ExtensionUIDialogOptions) (json.RawMessage, error)
	EditorValue(ctx context.Context, title string, prefill *string) (json.RawMessage, error)
}

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

	// SetStatus sets status text in the footer/status bar. Empty text clears the entry, except in RPC mode, which writes it: Pi writes the text as given
	// there and `undefined` removes the entry, so a UIContext that implements [StatusClearer] receives the `undefined` form as ClearStatus.
	// upstream: types.ts:139
	SetStatus(key, text string)

	// SetWorkingMessage sets the working/loading message shown during streaming. A UIContext that implements [WorkingMessageClearer] shows an empty
	// message as an empty label, as Pi's `message ?? defaultWorkingMessage` does (interactive-mode.ts:2641-2646); one that does not treats
	// an empty message as the default, as before.
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

	// SetWidget sets a widget of text lines to display above or below the editor; nil lines remove it. opts describes placement.
	//
	// upstream: types.ts:188 (the string[] overload of setWidget; the component-factory overload, types.ts:189, is SetWidgetFactory).
	SetWidget(key string, content []string, opts *ExtensionWidgetOptions)

	// SetWidgetFactory sets a widget built by factory to display above or below the editor; a nil factory removes it. It is setWidget's
	// component-factory overload (types.ts:189), which Go names apart because a Go method cannot be overloaded.
	// upstream: types.ts:189
	SetWidgetFactory(key string, factory WidgetFactory, opts *ExtensionWidgetOptions)

	// SetFooter installs a custom footer factory. Nil restores the
	// built-in footer.
	// upstream: types.ts:172
	SetFooter(factory FooterFactory)

	// SetHeader installs a custom header factory. Nil restores the
	// built-in header.
	// upstream: types.ts:181
	SetHeader(factory HeaderFactory)

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
	//
	// An extension in the host process passes a [CustomFactory] and, optionally,
	// [CustomOptions]; the call blocks until the factory's done callback returns
	// the result.
	Custom(ctx context.Context, factory CustomFactory, opts *CustomOptions) (any, error)

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
	AddAutocompleteProvider(factory AutocompleteProviderFactory) error

	// SetEditorComponent installs the editor factory's component in place of the default editor, as Pi's setCustomEditorComponent does
	// (it calls the factory with the TUI, the editor theme and the keybindings). Nil restores the default editor.
	// upstream: types.ts:278
	SetEditorComponent(factory EditorFactory)

	// GetEditorComponent returns the factory the last SetEditorComponent installed, nil when the default editor is active.
	// upstream: types.ts:281 (interactive-mode.ts:2664 `getEditorComponent: () => this.editorComponentFactory`)
	GetEditorComponent() EditorFactory

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
	SetTheme(theme ThemeSelection) SetThemeResult

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

// ExtensionUIContext is upstream's name for [UIContext] (types.ts:120).
type ExtensionUIContext = UIContext

// noopUIContext is the per-package no-op UI context returned when
// nothing has been bound. Mirrors upstream's `noOpUIContext` constant
// at runner.ts:188-217.
//
// Every method returns the upstream-equivalent value for an unavailable UI:
// empty input, false confirmation, no-op mutation, or an empty collection.
type noopUIContext struct{}

// NoopUIContext is the package-level no-op singleton. Hosts that have
// no UI (print and JSON modes) wire `ContextActions.UI = NoopUIContext`;
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

// noopThemeProvider is the active-theme source for [NoopUIContext.Theme]. The theme lives in a package that imports this one, so that package registers it
// with [SetNoopUITheme] at init; until then no theme exists to hand out.
var noopThemeProvider atomic.Pointer[func() Theme]

// SetNoopUITheme registers the function [NoopUIContext].Theme answers from. Pi's no-op UI context (runner.ts noOpUIContext) and its RPC UI both hand an
// extension the global theme as `ctx.ui.theme`, so an in-process extension running headless reads the same palette the TUI would. A nil provider unregisters.
func SetNoopUITheme(provider func() Theme) {
	if provider == nil {
		noopThemeProvider.Store(nil)
		return
	}
	noopThemeProvider.Store(&provider)
}

func noopUITheme() Theme {
	if provider := noopThemeProvider.Load(); provider != nil {
		return (*provider)()
	}
	return nil
}

func (*noopUIContext) Select(context.Context, string, []string, ExtensionUIDialogOptions) (string, error) {
	return "", nil
}
func (*noopUIContext) Confirm(context.Context, string, string, ExtensionUIDialogOptions) (bool, error) {
	return false, nil
}
func (*noopUIContext) Input(context.Context, string, string, ExtensionUIDialogOptions) (string, error) {
	return "", nil
}
func (*noopUIContext) Notify(string, string)                                           {}
func (*noopUIContext) OnTerminalInput(TerminalInputHandler) func()                     { return func() {} }
func (*noopUIContext) SetStatus(string, string)                                        {}
func (*noopUIContext) SetWorkingMessage(string)                                        {}
func (*noopUIContext) SetWorkingVisible(bool)                                          {}
func (*noopUIContext) SetWorkingIndicator(WorkingIndicatorOptions)                     {}
func (*noopUIContext) SetHiddenThinkingLabel(string)                                   {}
func (*noopUIContext) SetWidget(string, []string, *ExtensionWidgetOptions)             {}
func (*noopUIContext) SetWidgetFactory(string, WidgetFactory, *ExtensionWidgetOptions) {}
func (*noopUIContext) SetFooter(FooterFactory)                                         {}
func (*noopUIContext) SetHeader(HeaderFactory)                                         {}
func (*noopUIContext) SetLogin(LoginDefinition) error                                  { return ErrUIUnavailable }
func (*noopUIContext) SetTitle(string)                                                 {}
func (*noopUIContext) Custom(context.Context, CustomFactory, *CustomOptions) (any, error) {
	return nil, nil
}
func (*noopUIContext) PasteToEditor(string)                                      {}
func (*noopUIContext) SetEditorText(string)                                      {}
func (*noopUIContext) GetEditorText() string                                     { return "" }
func (*noopUIContext) Editor(context.Context, string, string) (string, error)    { return "", nil }
func (*noopUIContext) AddAutocompleteProvider(AutocompleteProviderFactory) error { return nil }
func (*noopUIContext) SetEditorComponent(EditorFactory)                          {}
func (*noopUIContext) GetEditorComponent() EditorFactory                         { return nil }
func (*noopUIContext) Theme() Theme                                              { return noopUITheme() }
func (*noopUIContext) GetAllThemes() []ThemeMeta                                 { return nil }
func (*noopUIContext) GetTheme(string) (Theme, error)                            { return nil, nil }
func (*noopUIContext) SetTheme(ThemeSelection) SetThemeResult {
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
