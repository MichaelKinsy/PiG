package tui

// extension_input.go: extension text-input wrapper.
//
// Ports upstream interactive/components/extension-input.ts. This wraps the
// bare TextInput surface with title, key hints, and horizontal borders for
// editor-slot extension prompts while keeping the underlying input semantics in
// text_input.go aligned with pi-tui's Input component.

// ExtensionInputComponent wraps a bare TextInput with extension-style chrome.
type ExtensionInputComponent struct {
	invalidatable
	input *TextInput
	title string
}

// NewExtensionInputComponent creates the editor-slot extension input wrapper.
// Placeholder is accepted for API parity; upstream currently ignores it.
func NewExtensionInputComponent(title, placeholder string) *ExtensionInputComponent {
	_ = placeholder
	return &ExtensionInputComponent{
		input: NewTextInput(""),
		title: title,
	}
}

// Done reports whether the user submitted or cancelled the input.
func (e *ExtensionInputComponent) Done() bool { return e.input.Done() }

// Cancelled reports whether the user cancelled the input.
func (e *ExtensionInputComponent) Cancelled() bool { return e.input.Cancelled() }

// Text returns the current value.
func (e *ExtensionInputComponent) Text() string { return e.input.Text() }

// SetText pre-fills the input.
func (e *ExtensionInputComponent) SetText(s string) { e.input.SetText(s) }

// HandleInput delegates to the wrapped input.
func (e *ExtensionInputComponent) HandleInput(data string) {
	e.input.HandleInput(data)
	e.Invalidate()
}

// Render mirrors upstream ExtensionInputComponent layout: the title and the
// key hints are Text(..., 1, 0) children, so they wrap within the width.
func (e *ExtensionInputComponent) Render(width int) []string {
	th := ActiveTheme()
	accent := th.Accent
	if accent == "" {
		accent = "\x1b[38;2;138;190;183m"
	}
	border := NewDynamicBorder("")
	// Upstream resolves tui.select.confirm → "enter", tui.select.cancel → "escape/ctrl+c".
	hint := RawKeyHint("enter", "submit") + "  " + RawKeyHint("escape/ctrl+c", "cancel")

	var lines []string
	lines = append(lines, border.Render(width)...)
	lines = append(lines, "")
	lines = append(lines, NewPaddedText(accent+e.title+th.Reset, 1, 0, nil).Render(width)...)
	lines = append(lines, "")
	lines = append(lines, e.input.Render(width)...)
	lines = append(lines, "")
	lines = append(lines, NewPaddedText(hint, 1, 0, nil).Render(width)...)
	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)
	return lines
}
