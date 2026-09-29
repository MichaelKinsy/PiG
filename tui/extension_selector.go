package tui

// Ports packages/coding-agent/src/modes/interactive/components/extension-selector.ts

import (
	"strconv"
	"strings"
)

// ExtensionSelectorComponent is the editor-slot overlay component
// extensions and built-in flows use to ask the user to pick from a
// list of string options.
type ExtensionSelectorComponent struct {
	invalidatable
	title                 string
	baseTitle             string
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
		baseTitle:             title,
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

// SetCountdown shows the seconds left before the selector times out, as
// upstream's countdown sets the title to `${baseTitle} (${s}s)`.
func (e *ExtensionSelectorComponent) SetCountdown(seconds int) {
	e.title = countdownTitle(e.baseTitle, seconds)
	e.Invalidate()
}

// Cancel completes the selector as cancelled, as upstream's countdown expiry
// calls onCancel.
func (e *ExtensionSelectorComponent) Cancel() {
	if e.done {
		return
	}
	e.done = true
	e.cancel = true
	e.Invalidate()
}

// Done reports whether the user picked an option (or cancelled).
func (e *ExtensionSelectorComponent) Done() bool { return e.done }

// Cancelled reports whether the cancellation action completed the selector.
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
	lines = append(lines, text(t.FgText("accent", boldText(e.title)))...)
	lines = append(lines, dialogDescriptionLines(e.description, width)...)
	lines = append(lines, "")

	for i, opt := range e.options {
		if i == e.cursor {
			lines = append(lines, text(t.FgText("accent", "→ ")+t.FgText("accent", opt))...)
		} else {
			lines = append(lines, text("  "+t.FgText("text", opt))...)
		}
	}

	lines = append(lines, "")
	hint := rawArrowHint() + "  " + extensionActionHint(KBSelectConfirm, "select") + "  " + extensionActionHint(KBSelectCancel, "cancel")
	lines = append(lines, text(hint)...)
	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)
	return lines
}

// HandleInput resolves expansion, navigation, confirmation and cancellation in that order. Empty options do not complete the selector.
func (e *ExtensionSelectorComponent) HandleInput(data string) {
	if e.done {
		return
	}
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, "app.tools.expand"):
		if e.onToggleToolsExpanded != nil {
			e.onToggleToolsExpanded()
		}
	case kb.Matches(data, KBSelectUp) || data == "k":
		e.cursor = max(0, e.cursor-1)
	case kb.Matches(data, KBSelectDown) || data == "j":
		e.cursor = min(len(e.options)-1, e.cursor+1)
	case kb.Matches(data, KBSelectConfirm) || data == "\n":
		if e.SelectedValue() != "" {
			e.done = true
		}
	case kb.Matches(data, KBSelectCancel):
		e.done = true
		e.cancel = true
	}
	e.Invalidate()
}

func extensionActionHint(action, label string) string {
	keys := GetTUIKeybindings().GetKeys(action)
	return extKeyHint(FormatKeyText(strings.Join(keys, "/"), false), label)
}

// rawArrowHint formats the navigation key in dim and its description in muted.
func rawArrowHint() string {
	return extKeyHint("↑↓", "navigate")
}

// extKeyHint formats an already-resolved key in dim and its description in muted, as upstream keyHint does.
func extKeyHint(key, label string) string {
	t := ActiveTheme()
	return t.FgText("dim", key) + t.FgText("muted", " "+label)
}

// dialogDescriptionLines renders an optional dialog description as upstream
// does: a blank spacer row, then the text in the theme's text color, wrapped
// with one cell of horizontal padding.
func dialogDescriptionLines(description string, width int) []string {
	if description == "" {
		return nil
	}
	t := ActiveTheme()
	return append([]string{""}, NewPaddedText(t.FgText("text", description), 1, 0, nil).Render(width)...)
}

func countdownTitle(title string, seconds int) string {
	return title + " (" + strconv.Itoa(seconds) + "s)"
}
