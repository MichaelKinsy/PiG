package tui

// extension_editor.go: multi-line editor overlay for extensions.
//
// Faithful port of upstream extension-editor.ts (132 LOC).
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

// ExtensionEditorComponent wraps a fresh Editor with bordered
// extension-editor chrome, matching upstream's layout exactly.
type ExtensionEditorComponent struct {
	invalidatable
	editor      *Editor
	title       string
	description string
	done        bool
	cancel      bool
	value       string
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
	// No spacer here: the Editor's Render starts with a blank line
	// ("one blank row above the top border") that provides the gap.
	lines = append(lines, c.editor.Render(width)...)
	lines = append(lines, "")

	// Hint line: matches upstream's keyHint output.
	// Upstream resolves: tui.select.confirm → "enter",
	// tui.input.newLine → "shift+enter/ctrl+j", tui.select.cancel → "escape/ctrl+c".
	hint := KeyHint("enter", "submit") + "  " +
		KeyHint("shift+enter/ctrl+j", "newline") + "  " +
		KeyHint("escape/ctrl+c", "cancel")
	lines = append(lines, NewPaddedText(hint, 1, 0, nil).Render(width)...)
	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)

	return lines
}
