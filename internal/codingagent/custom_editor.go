package codingagent

import "github.com/MichaelKinsy/PiG/tui"

// Ports packages/coding-agent/src/modes/interactive/components/custom-editor.ts
//
// CustomEditor is the default editor with the coding agent's app keybindings (custom-editor.ts CustomEditor extends Editor). Handlers run in the
// order they were registered, as the Map in upstream iterates; an Editor key none of them takes goes to superInput, which is the editor's own handling.
type CustomEditor struct {
	*tui.Editor
	keybindings *KeybindingsManager

	// actionHandlers lists the registered app actions in registration order.
	actionHandlers []customEditorAction

	// OnEscape, OnCtrlD and OnPasteImage replace the handlers registered for app.interrupt, app.exit and app.clipboard.pasteImage while set.
	OnEscape     func()
	OnCtrlD      func()
	OnPasteImage func()
	// OnExtensionShortcut handles extension-registered shortcuts and reports whether it consumed the key.
	OnExtensionShortcut func(data string) bool

	// superInput is Editor.handleInput for the keys the app actions do not take.
	superInput func(data string)
}

type customEditorAction struct {
	action  AppKeybinding
	handler func()
}

// NewCustomEditor is upstream's `new CustomEditor(tui, theme, keybindings, options)`: a default editor built with options and theme, with the
// app keybindings, whose own input handling (upstream's super.handleInput) takes the keys no app action does. Go's Editor takes no TUI: it
// renders through the component tree, so the TUI argument is the one the editor is mounted in and is not read.
// upstream: custom-editor.ts:24-27 constructor(tui, theme, keybindings, options?)
func NewCustomEditor(ui tui.TUI, theme tui.EditorTheme, keybindings *KeybindingsManager, options CustomEditorOptions) *CustomEditor {
	if options.UI == nil {
		options.UI = ui
	}
	var editorOptions []tui.EditorOption
	if theme.BorderColor != nil {
		// A zero theme leaves the editor on the active theme, as the editor does when built without one.
		editorOptions = append(editorOptions, tui.WithEditorTheme(theme))
	}
	editor := NewEditorWithOptions(options, editorOptions...)
	return wrapCustomEditor(editor, keybindings, editor.HandleInput, options)
}

// wrapCustomEditor wraps editor with the app keybindings. superInput is what upstream's super.handleInput does for a key the app actions leave.
// options.EmbedWorkingStatus is the editor's readonly embedWorkingStatus (custom-editor.ts:25 `options?.embedWorkingStatus ?? false`).
func wrapCustomEditor(editor *tui.Editor, keybindings *KeybindingsManager, superInput func(data string), options CustomEditorOptions) *CustomEditor {
	if options.PaddingX != nil {
		editor.SetPaddingX(*options.PaddingX)
	}
	if options.AutocompleteMaxVisible != nil {
		editor.SetAutocompleteMaxVisible(*options.AutocompleteMaxVisible)
	}
	editor.EmbedWorkingStatus = options.EmbedWorkingStatus
	return &CustomEditor{Editor: editor, keybindings: keybindings, superInput: superInput}
}

// OnAction registers a handler for an app action (custom-editor.ts onAction); registering an action again replaces its handler and keeps its place.
func (e *CustomEditor) OnAction(action AppKeybinding, handler func()) {
	for i := range e.actionHandlers {
		if e.actionHandlers[i].action == action {
			e.actionHandlers[i].handler = handler
			return
		}
	}
	e.actionHandlers = append(e.actionHandlers, customEditorAction{action: action, handler: handler})
}

// ActionHandler returns the handler registered for an app action.
func (e *CustomEditor) ActionHandler(action AppKeybinding) (func(), bool) {
	for _, entry := range e.actionHandlers {
		if entry.action == action {
			return entry.handler, true
		}
	}
	return nil, false
}

// HandleInput is custom-editor.ts handleInput: extension shortcuts first, then the clipboard image paste, interrupt, exit, the explicit editor
// history bindings, every other app action in registration order, and finally the editor itself.
func (e *CustomEditor) HandleInput(data string) {
	if e.keybindings == nil {
		// A mode built without a keybinding table classifies keys by the built-in defaults, which superInput does.
		e.superInput(data)
		return
	}
	if e.OnExtensionShortcut != nil && e.OnExtensionShortcut(data) {
		return
	}

	if e.keybindings.Matches(data, string(AppClipboardPasteImage)) {
		if e.OnPasteImage != nil {
			e.OnPasteImage()
		}
		return
	}

	if e.keybindings.Matches(data, string(AppInterrupt)) {
		if !e.AutocompleteOpen() {
			handler := e.OnEscape
			if handler == nil {
				handler, _ = e.ActionHandler(AppInterrupt)
			}
			if handler != nil {
				handler()
				return
			}
		}
		e.superInput(data)
		return
	}

	if e.keybindings.Matches(data, string(AppExit)) {
		if e.Text() == "" {
			handler := e.OnCtrlD
			if handler == nil {
				handler, _ = e.ActionHandler(AppExit)
			}
			if handler != nil {
				handler()
			}
			return
		}
	}

	if e.keybindings.MatchesEditorHistory(data) {
		e.superInput(data)
		return
	}

	for _, entry := range e.actionHandlers {
		if entry.action != AppInterrupt && entry.action != AppExit && e.keybindings.Matches(data, string(entry.action)) {
			entry.handler()
			return
		}
	}

	e.superInput(data)
}

// CustomEditorOptions is custom-editor.ts:5-8 CustomEditorOptions = EditorOptions & { embedWorkingStatus?: boolean }. A nil field is an omitted option.
type CustomEditorOptions struct {
	// PaddingX is the editor's horizontal padding; omitted is 0 and a negative value is 0.
	PaddingX *int
	// AutocompleteMaxVisible is the most autocomplete rows shown; omitted is 5, and the value is kept within 3 to 20.
	AutocompleteMaxVisible *int
	// EmbedWorkingStatus renders working, compaction, summarization and retry status in the editor's top border; omitted is false.
	EmbedWorkingStatus bool
	// UI is the TUI the editor owns (editor.ts constructor's tui); nil builds the host-driven editor.
	UI tui.TUI
}

// NewEditorWithOptions is the editor that `new CustomEditor(tui, theme, keybindings, options)` builds (editor.ts:373-381 and custom-editor.ts:26-30): the options set the padding, the autocomplete row limit and whether status is embedded in the border.
func NewEditorWithOptions(options CustomEditorOptions, editorOptions ...tui.EditorOption) *tui.Editor {
	var editor *tui.Editor
	if options.UI != nil {
		editor = tui.NewEditorWithTUI(options.UI, tui.EditorTheme{}, tui.EditorOptions{PaddingX: options.PaddingX, AutocompleteMaxVisible: options.AutocompleteMaxVisible})
	} else {
		editor = tui.NewEditor(editorOptions...)
	}
	if options.PaddingX != nil {
		editor.SetPaddingX(*options.PaddingX)
	}
	if options.AutocompleteMaxVisible != nil {
		editor.SetAutocompleteMaxVisible(*options.AutocompleteMaxVisible)
	}
	editor.EmbedWorkingStatus = options.EmbedWorkingStatus
	return editor
}
