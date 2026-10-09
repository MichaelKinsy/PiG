package codingagent

import (
	"github.com/MichaelKinsy/PiG/tui"
)

// ExtensionEditorOptions is extension-editor.ts ExtensionEditorOptions: the Editor's options and an optional description shown between the title and the editor.
type ExtensionEditorOptions struct {
	tui.EditorOptions
	Description string
}

// ExtensionEditorComponent is the multi-line editor overlay of ctx.ui.editor() and the /tree summary prompt (extension-editor.ts ExtensionEditorComponent).
//
// Layout: DynamicBorder, Spacer(1), Text(accent title), [Spacer(1), Text(description)], Spacer(1), Editor, Spacer(1), Text(hint), Spacer(1), DynamicBorder.
//
// Ports packages/coding-agent/src/modes/interactive/components/extension-editor.ts
type ExtensionEditorComponent struct {
	tui.Container
	editor      *tui.Editor
	ui          tui.TUI
	keybindings *KeybindingsManager
	onSubmit    func(value string)
	onCancel    func()
	focused     bool
	done        bool
	// externalEditorCommand is the command the external-editor handoff runs (constructor argument externalEditorCommand, already defaulted by the caller).
	externalEditorCommand string
	externalEditor        func(command, content string, apply func(string))
}

// NewExtensionEditorComponent is extension-editor.ts constructor(tui, keybindings, title, prefill, onSubmit, onCancel, options, externalEditorCommand).
// onSubmit receives the editor's text when Enter submits; onCancel runs on the cancel key. After either has run the component ignores input, because its
// caller removes it in the same turn. options may be nil. externalEditorCommand is what the handoff bound by [ExtensionEditorComponent.SetExternalEditor]
// receives; empty leaves the choice to the handoff.
func NewExtensionEditorComponent(ui tui.TUI, keybindings *KeybindingsManager, title, prefill string, onSubmit func(string), onCancel func(), options *ExtensionEditorOptions, externalEditorCommand string) *ExtensionEditorComponent {
	c := &ExtensionEditorComponent{ui: ui, keybindings: keybindings, onSubmit: onSubmit, onCancel: onCancel, externalEditorCommand: externalEditorCommand}
	var description string
	var editorOptions tui.EditorOptions
	if options != nil {
		description, editorOptions = options.Description, options.EditorOptions
	}
	theme := tui.ActiveTheme()
	c.Add(tui.NewDynamicBorder())
	c.Add(tui.NewSpacer(1))
	c.Add(tui.NewPaddedText(theme.Accent+title+theme.Reset, 1, 0, nil))
	if description != "" {
		c.Add(tui.NewSpacer(1))
		c.Add(tui.NewPaddedText(theme.Fg("text", description), 1, 0, nil))
	}
	c.Add(tui.NewSpacer(1))
	// The host renders the editor (it holds the TUI's render lock), so the editor takes its visible-line limit from the host rather than from ui.Terminal().
	c.editor = tui.NewEditor()
	c.editor.SetMaxVisibleLines(5)
	if editorOptions.PaddingX != nil {
		c.editor.SetPaddingX(*editorOptions.PaddingX)
	}
	if editorOptions.AutocompleteMaxVisible != nil {
		c.editor.SetAutocompleteMaxVisible(*editorOptions.AutocompleteMaxVisible)
	}
	if prefill != "" {
		c.editor.SetText(prefill)
	}
	// Enter submits; Shift+Enter inserts a newline, as in the main editor.
	c.editor.OnSubmit = func(text string) {
		c.done = true
		if c.onSubmit != nil {
			c.onSubmit(text)
		}
	}
	c.Add(c.editor)
	c.Add(tui.NewSpacer(1))
	// Upstream resolves tui.select.confirm to "enter", tui.input.newLine to "shift+enter/ctrl+j" and tui.select.cancel to "escape/ctrl+c".
	hint := tui.KeyHint("enter", "submit") + "  " + tui.KeyHint("shift+enter/ctrl+j", "newline") + "  " + tui.KeyHint("escape/ctrl+c", "cancel") +
		"  " + tui.KeyHint(tui.AppKeyText("app.editor.external", "ctrl+g"), "external editor")
	c.Add(tui.NewPaddedText(hint, 1, 0, nil))
	c.Add(tui.NewSpacer(1))
	c.Add(tui.NewDynamicBorder())
	return c
}

// Focused reports the Focusable flag (extension-editor.ts get focused).
func (c *ExtensionEditorComponent) Focused() bool { return c.focused }

// SetFocused implements Focusable: the flag propagates to the inner editor, which emits the hardware-cursor marker only while focused (extension-editor.ts set focused).
func (c *ExtensionEditorComponent) SetFocused(focused bool) {
	c.focused = focused
	c.editor.SetFocused(focused)
}

// SetExternalEditor binds the terminal owner's asynchronous external-editor handoff, which hands the terminal to a child running command on content. A
// successful completion calls apply, which replaces the editor's text without submitting the dialog. Upstream does the handoff inside the component
// (ui.stop, editInExternalEditor, ui.start); the terminal owner here also owns raw mode and the input reader, so it runs the child.
func (c *ExtensionEditorComponent) SetExternalEditor(open func(command, content string, apply func(string))) {
	c.externalEditor = open
}

// HandleInput is extension-editor.ts handleInput: the cancel key calls onCancel, the external-editor key opens the handoff, and everything else goes to the editor.
func (c *ExtensionEditorComponent) HandleInput(data string) {
	if c.done {
		return
	}
	if tui.GetTUIKeybindings().Matches(data, tui.KBSelectCancel) {
		c.done = true
		if c.onCancel != nil {
			c.onCancel()
		}
		c.Invalidate()
		return
	}
	if c.matchesExternalEditor(data) && c.externalEditor != nil {
		c.externalEditor(c.externalEditorCommand, c.editor.Text(), func(text string) {
			if !c.done {
				c.editor.SetText(text)
				c.Invalidate()
			}
			c.ui.RequestRender(true)
		})
		return
	}
	c.editor.HandleInput(data)
	c.Invalidate()
}

// matchesExternalEditor is `this.keybindings.matches(keyData, "app.editor.external")`. A host without a manager (a test harness) resolves the action through
// the merged manager the tui package holds, which the coding-agent manager installs.
func (c *ExtensionEditorComponent) matchesExternalEditor(data string) bool {
	if c.keybindings == nil {
		return tui.GetTUIKeybindings().Matches(data, "app.editor.external")
	}
	return c.keybindings.Matches(data, "app.editor.external")
}
