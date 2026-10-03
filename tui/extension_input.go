package tui

// Ports packages/coding-agent/src/modes/interactive/components/extension-input.ts

// ExtensionInputComponent wraps a bare TextInput with extension-style chrome.
type ExtensionInputComponent struct {
	invalidatable
	input     *TextInput
	title     string
	baseTitle string
	done      bool
	cancelled bool

	// The title and hint are built when the component is constructed (and the title for a countdown tick), as upstream builds each Text with theme.fg; a theme change leaves them as built.
	titleText string
	hintText  string
}

// NewExtensionInputComponent creates the editor-slot extension input wrapper.
// Placeholder is accepted for API parity; upstream currently ignores it.
func NewExtensionInputComponent(title, placeholder string) *ExtensionInputComponent {
	_ = placeholder
	input := NewInput(InputOptions{})
	// This host wrapper occupies the active editor slot.
	input.Focused = true
	return &ExtensionInputComponent{
		input:     input,
		title:     title,
		baseTitle: title,
		titleText: ActiveTheme().FgText("accent", title),
		hintText:  extensionActionHint(KBSelectConfirm, "submit") + "  " + extensionActionHint(KBSelectCancel, "cancel"),
	}
}

// SetCountdown shows the seconds left before the input times out, as
// upstream's countdown sets the title to `${baseTitle} (${s}s)`.
func (e *ExtensionInputComponent) SetCountdown(seconds int) {
	e.title = countdownTitle(e.baseTitle, seconds)
	e.titleText = ActiveTheme().FgText("accent", e.title)
	e.Invalidate()
}

// Cancel completes the input as cancelled, as upstream's countdown expiry calls
// onCancel.
func (e *ExtensionInputComponent) Cancel() {
	if e.done {
		return
	}
	e.done = true
	e.cancelled = true
	e.Invalidate()
}

// Done reports whether the user submitted or cancelled the input.
func (e *ExtensionInputComponent) Done() bool { return e.done }

// Cancelled reports whether the user cancelled the input.
func (e *ExtensionInputComponent) Cancelled() bool { return e.cancelled }

// Text returns the current value.
func (e *ExtensionInputComponent) Text() string { return e.input.Text() }

// SetText pre-fills the input.
func (e *ExtensionInputComponent) SetText(s string) { e.input.SetText(s) }

// HandleInput resolves selection actions before delegating text editing to Input. The inner input's submit action does not complete the dialog.
func (e *ExtensionInputComponent) HandleInput(data string) {
	if e.done {
		return
	}
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectConfirm) || data == "\n":
		e.done = true
	case kb.Matches(data, KBSelectCancel):
		e.done = true
		e.cancelled = true
	default:
		e.input.HandleInput(data)
	}
	e.Invalidate()
}

// Render mirrors upstream ExtensionInputComponent layout: the title and the
// key hints are Text(..., 1, 0) children, so they wrap within the width.
func (e *ExtensionInputComponent) Render(width int) []string {
	border := NewDynamicBorder("")

	var lines []string
	lines = append(lines, border.Render(width)...)
	lines = append(lines, "")
	lines = append(lines, NewPaddedText(e.titleText, 1, 0, nil).Render(width)...)
	lines = append(lines, "")
	lines = append(lines, e.input.Render(width)...)
	lines = append(lines, "")
	lines = append(lines, NewPaddedText(e.hintText, 1, 0, nil).Render(width)...)
	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)
	return lines
}
