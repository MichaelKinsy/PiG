package tui

// extension_selector.go: generic extension selector overlay.
//
// Faithful port of upstream extension-selector.ts:
//
//   DynamicBorder
//   Spacer(1)
//   Text(theme.fg("accent", theme.bold(title)), 1, 0)
//   [Spacer(1) + Text(theme.fg("text", description), 1, 0)]
//   Spacer(1)
//   Container[ Text("→ option", 1, 0) | Text("  option", 1, 0) ... ]
//   Spacer(1)
//   Text("↑↓ navigate  enter select  escape/ctrl+c cancel", 1, 0)
//   Spacer(1)
//   DynamicBorder
//
// Used by the interactive mode's ShowExtensionSelector slash-handler
// callback (for /tree "Summarize branch?", "Yes/No" confirms via
// extension ui.confirm, ui.select dialogs from extensions, etc.).
//
// Differs from FilterableList by design: no search input box, no
// `(N/M)` scroll indicator, no per-item description column.

// ExtensionSelectorComponent is the editor-slot overlay component
// extensions and built-in flows use to ask the user to pick from a
// list of string options.
type ExtensionSelectorComponent struct {
	invalidatable
	title                 string
	description           string
	options               []string
	cursor                int
	done                  bool
	cancel                bool
	onToggleToolsExpanded func()
}

// NewExtensionSelector creates a generic selector overlay. The first
// option is pre-selected. The component does not own its lifecycle -
// the caller (runEditorSlotExtensionSelector) drives input/render
// until Done() returns true.
func NewExtensionSelector(title string, options []string, onToggleToolsExpanded ...func()) *ExtensionSelectorComponent {
	var toggle func()
	if len(onToggleToolsExpanded) > 0 {
		toggle = onToggleToolsExpanded[0]
	}
	return &ExtensionSelectorComponent{
		title:                 title,
		options:               options,
		onToggleToolsExpanded: toggle,
	}
}

// SetDescription sets optional explanatory text shown between the title and
// the options. Mirrors upstream ExtensionSelectorOptions.description.
func (e *ExtensionSelectorComponent) SetDescription(description string) {
	e.description = description
	e.Invalidate()
}

// Done reports whether the user picked an option (or cancelled).
func (e *ExtensionSelectorComponent) Done() bool { return e.done }

// Cancelled reports whether Esc was pressed.
func (e *ExtensionSelectorComponent) Cancelled() bool { return e.cancel }

// SelectedIndex returns the index of the selected option, or -1 if
// cancelled.
func (e *ExtensionSelectorComponent) SelectedIndex() int {
	if e.cancel {
		return -1
	}
	return e.cursor
}

// SelectedValue returns the selected option string, or "" if cancelled.
func (e *ExtensionSelectorComponent) SelectedValue() string {
	if e.cancel || e.cursor < 0 || e.cursor >= len(e.options) {
		return ""
	}
	return e.options[e.cursor]
}

// Render mirrors upstream extension-selector.ts, whose rows are Text(…, 1, 0)
// children between Spacer(1) and DynamicBorder rows:
//
//	DynamicBorder + Spacer + accent(bold(title)) + [Spacer + description] +
//	Spacer + one Text per option ("→ " prefix on selected) + Spacer +
//	navigate/select/cancel hint + Spacer + DynamicBorder.
//
// Every Text wraps within one cell of padding on each side and pads to width,
// so no row is wider than the render width.
func (e *ExtensionSelectorComponent) Render(width int) []string {
	t := ActiveTheme()
	border := NewDynamicBorder("")
	text := func(content string) []string { return NewPaddedText(content, 1, 0, nil).Render(width) }

	var lines []string
	lines = append(lines, border.Render(width)...)
	lines = append(lines, "")
	lines = append(lines, text(t.Accent+"\x1b[1m"+e.title+SGRBoldDimReset+t.Reset)...)
	lines = append(lines, dialogDescriptionLines(e.description, width)...)
	lines = append(lines, "")

	for i, opt := range e.options {
		if i == e.cursor {
			lines = append(lines, text(t.Accent+"→ "+t.Reset+t.Accent+opt+t.Reset)...)
		} else {
			lines = append(lines, text("  "+t.Text+opt+t.Reset)...)
		}
	}

	lines = append(lines, "")
	hint := rawArrowHint() + "  " + extKeyHint("enter", "select") + "  " + extKeyHint("escape/ctrl+c", "cancel")
	lines = append(lines, text(hint)...)
	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)
	return lines
}

// HandleInput processes navigation, select, and cancel keys. Matches
// upstream's handleInput: Up/Down/k/j navigate, Enter selects, Esc
// cancels.
func (e *ExtensionSelectorComponent) HandleInput(data string) {
	if e.done {
		return
	}
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectCancel):
		e.done = true
		e.cancel = true
	case kb.Matches(data, KBSelectUp) || data == "k":
		if e.cursor > 0 {
			e.cursor--
		}
	case kb.Matches(data, KBSelectDown) || data == "j":
		if e.cursor < len(e.options)-1 {
			e.cursor++
		}
	case kb.Matches(data, KBSelectConfirm) || data == "\n":
		e.done = true
	// app.tools.expand is an app-level binding (default Ctrl+O), not part of the
	// TUI keybinding registry. Extension selectors live in modal editor-slot flows
	// and upstream routes this action through the interactive-mode app bindings, so
	// preserve the default byte sequence here.
	case matchesAppToolsExpand(data):
		if e.onToggleToolsExpanded != nil {
			e.onToggleToolsExpanded()
		}
	}
	e.Invalidate()
}

func matchesAppToolsExpand(data string) bool {
	return data == "\x0f"
}

// rawArrowHint formats the "↑↓ navigate" hint exactly like upstream
// rawKeyHint("↑↓", "navigate") with muted-label theming.
func rawArrowHint() string {
	t := ActiveTheme()
	return t.Muted + "↑↓" + t.Reset + t.Muted + " navigate" + t.Reset
}

// extKeyHint matches upstream's keyHint(id, label) by formatting
// "<key> <label>" with the same muted theming. Used for the hint
// line in the extension selector. The keys passed in already match
// upstream's resolved keybinding names ("enter", "escape/ctrl+c").
func extKeyHint(key, label string) string {
	t := ActiveTheme()
	return t.Muted + key + t.Reset + t.Muted + " " + label + t.Reset
}

// dialogDescriptionLines renders an optional dialog description as upstream
// does: a blank spacer row, then the text in the theme's text color, wrapped
// with one cell of horizontal padding.
func dialogDescriptionLines(description string, width int) []string {
	if description == "" {
		return nil
	}
	t := ActiveTheme()
	return append([]string{""}, NewPaddedText(t.Text+description+t.Reset, 1, 0, nil).Render(width)...)
}
