package tui

// extension_editor.go: multi-line editor overlay for extensions.
//
// Layout (matches upstream ExtensionEditorComponent):
//
//   DynamicBorder
//   Spacer(1)
//   Text(theme.fg("accent", title))   : indented 1
//   Spacer(1)
//   Editor                            : multi-line, Enter submits, Shift+Enter/Ctrl+J newline
//   Spacer(1)
//   Text(hint)                        : indented 1
//   Spacer(1)
//   DynamicBorder
//
// Used by ShowExtensionEditor (interactive mode) and ExtUIContext.Editor
// for the /tree "Custom summarization instructions" flow and any
// extension ctx.ui.editor() calls.

// Ports packages/coding-agent/src/modes/interactive/components/extension-editor.ts
// ExtensionEditorComponent wraps a fresh Editor with bordered
// extension-editor chrome, matching upstream's layout exactly.
type ExtensionEditorComponent struct {
	invalidatable
	editor         *Editor
	title          string
	description    string
	done           bool
	cancel         bool
	value          string
	externalEditor func(string, func(string))
}

// NewExtensionEditorComponent creates the editor-slot extension editor.
// prefill pre-populates the editor content.
func NewExtensionEditorComponent(title, prefill string) *ExtensionEditorComponent {
	ed := NewEditor()
	// Extension editor uses a small max visible lines count since it
	// sits in the editor slot alongside chrome. Upstream defaults to
	// floor(terminalRows * 0.3) but the extension editor's Editor
	// instance is created with default options.
	ed.SetMaxVisibleLines(5)
	ed.Focused = true
	if prefill != "" {
		ed.SetText(prefill)
	}

	c := &ExtensionEditorComponent{
		editor: ed,
		title:  title,
	}

	// Wire Enter → submit via Editor.OnSubmit (mirrors upstream's
	// editor.onSubmit = (text) => { this.onSubmitCallback(text); }).
	ed.OnSubmit = func(text string) {
		c.value = text
		c.done = true
	}

	return c
}

// SetDescription sets optional explanatory text shown between the title and
// the editor. Mirrors upstream ExtensionEditorOptions.description.
func (c *ExtensionEditorComponent) SetDescription(description string) {
	c.description = description
	c.Invalidate()
}

// SetExternalEditor binds the terminal owner's asynchronous external-editor handoff. A successful completion updates the buffer without submitting the dialog.
func (c *ExtensionEditorComponent) SetExternalEditor(open func(string, func(string))) {
	c.externalEditor = open
}

// Done reports whether the user submitted or cancelled.
func (c *ExtensionEditorComponent) Done() bool { return c.done }

// Cancelled reports whether the user pressed Esc/Ctrl+C.
func (c *ExtensionEditorComponent) Cancelled() bool { return c.cancel }

// Value returns the submitted text (empty if cancelled).
func (c *ExtensionEditorComponent) Value() string {
	if c.cancel {
		return ""
	}
	return c.value
}

// HandleInput processes key events. Esc/Ctrl+C cancels; everything
// else is forwarded to the underlying Editor (which calls OnSubmit
// on Enter, handles Shift+Enter/Ctrl+J as newline).
func (c *ExtensionEditorComponent) HandleInput(data string) {
	if c.done {
		return
	}
	kb := GetTUIKeybindings()
	if kb.Matches(data, KBSelectCancel) {
		c.cancel = true
		c.done = true
		c.Invalidate()
		return
	}
	if kb.Matches(data, "app.editor.external") && c.externalEditor != nil {
		c.externalEditor(c.editor.Text(), func(text string) {
			if !c.done {
				c.editor.SetText(text)
				c.Invalidate()
			}
		})
		return
	}
	c.editor.HandleInput(data)
	c.Invalidate()
}

// Render mirrors upstream ExtensionEditorComponent layout:
//
//	DynamicBorder + Spacer + accent(title) indented 1 + Spacer +
//	Editor (with its own internal dashed borders) + Spacer +
//	hint line indented 1 + Spacer + DynamicBorder.
func (c *ExtensionEditorComponent) Render(width int) []string {
	t := ActiveTheme()
	border := NewDynamicBorder("")

	var lines []string
	lines = append(lines, border.Render(width)...)
	lines = append(lines, "")
	lines = append(lines, NewPaddedText(t.Accent+c.title+t.Reset, 1, 0, nil).Render(width)...)
	lines = append(lines, dialogDescriptionLines(c.description, width)...)
	lines = append(lines, "")
	lines = append(lines, c.editor.Render(width)...)
	lines = append(lines, "")

	// Hint line: matches upstream's keyHint output.
	// Upstream resolves: tui.select.confirm → "enter",
	// tui.input.newLine → "shift+enter/ctrl+j", tui.select.cancel → "escape/ctrl+c".
	hint := KeyHint("enter", "submit") + "  " +
		KeyHint("shift+enter/ctrl+j", "newline") + "  " +
		KeyHint("escape/ctrl+c", "cancel") + "  " +
		KeyHint(AppKeyText("app.editor.external", "ctrl+g"), "external editor")
	lines = append(lines, NewPaddedText(hint, 1, 0, nil).Render(width)...)
	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)

	return lines
}
