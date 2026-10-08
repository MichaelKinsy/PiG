package tui

// Ports packages/coding-agent/src/modes/interactive/components/extension-selector.ts

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// previewGapCells separates the longest option row from the preview column that follows it.
const previewGapCells = 4

// previewRightCells is the margin kept between the preview column and the dialog's right border.
const previewRightCells = 2

// previewFloorCells is the narrowest dialog body that keeps a preview column. Below it the rows would squeeze into a
// column of words, so the selector draws alone, as upstream's does.
const previewFloorCells = 40

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

	// The themed texts are built when they change, as upstream builds each Text with theme.fg: the title at
	// construction and for a countdown tick, the description at construction, the hint at construction and the
	// rows at construction and when the selection moves (updateList). A theme change leaves them as built; only
	// the borders are drawn with the theme of the moment.
	titleText       string
	descriptionText string
	hintText        string
	rowTexts        []string

	// preview draws the right column beside the option rows for the highlighted option, or is nil for upstream's
	// selector, which has no preview column.
	preview func(selectedIndex int) []string
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
	e := &ExtensionSelectorComponent{
		title:                 title,
		baseTitle:             title,
		options:               options,
		onToggleToolsExpanded: toggle,
	}
	e.titleText = styledDialogTitle(title)
	e.hintText = rawArrowHint() + "  " + extensionActionHint(KBSelectConfirm, "select") + "  " + extensionActionHint(KBSelectCancel, "cancel")
	e.updateList()
	return e
}

// styledDialogTitle is the title Text content of upstream's selector: theme.fg("accent", theme.bold(title)).
func styledDialogTitle(title string) string {
	return ActiveTheme().FgText("accent", boldText(title))
}

// updateList rebuilds the option rows with the current theme (extension-selector.ts updateList).
func (e *ExtensionSelectorComponent) updateList() {
	t := ActiveTheme()
	e.rowTexts = make([]string, len(e.options))
	for i, opt := range e.options {
		if i == e.cursor {
			e.rowTexts[i] = t.FgText("accent", "→ ") + t.FgText("accent", opt)
		} else {
			e.rowTexts[i] = "  " + t.FgText("text", opt)
		}
	}
}

// SetPreview supplies the dialog's right column: the lines drawn beside the option rows for the highlighted option,
// which the selector asks for on every render so the column follows the cursor. Nil, the default, draws upstream's
// selector with no preview column. Pig's sprite picker uses it to show the pig of the sprite under the cursor.
// pig divergence (D2): the preview column is Pig's sprite picker's; upstream's selector renders without one.
func (e *ExtensionSelectorComponent) SetPreview(preview func(selectedIndex int) []string) {
	e.preview = preview
	e.Invalidate()
}

// SetDescription sets optional explanatory text shown between the title and
// the options. Mirrors upstream ExtensionSelectorOptions.description.
func (e *ExtensionSelectorComponent) SetDescription(description string) {
	e.description = description
	e.descriptionText = ""
	if description != "" {
		e.descriptionText = ActiveTheme().FgText("text", description)
	}
	e.Invalidate()
}

// SetCountdown shows the seconds left before the selector times out, as
// upstream's countdown sets the title to `${baseTitle} (${s}s)`.
func (e *ExtensionSelectorComponent) SetCountdown(seconds int) {
	e.title = countdownTitle(e.baseTitle, seconds)
	e.titleText = styledDialogTitle(e.title)
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
// so no row is wider than the render width. With a preview (SetPreview) the
// option rows take the width of the longest row (wrapping only to keep the preview
// inside the dialog), the preview follows previewGapCells later, top-aligned with
// the first row, and the title, hint and borders keep the full width.
func (e *ExtensionSelectorComponent) Render(width int) []string {
	border := NewDynamicBorder("")
	text := func(content string) []string { return NewPaddedText(content, 1, 0, nil).Render(width) }

	var lines []string
	lines = append(lines, border.Render(width)...)
	lines = append(lines, "")
	lines = append(lines, text(e.titleText)...)
	lines = append(lines, styledDescriptionLines(e.descriptionText, width)...)
	lines = append(lines, "")

	preview := e.previewLines(width)
	if preview == nil {
		for _, row := range e.rowTexts {
			lines = append(lines, text(row)...)
		}
	} else {
		lines = append(lines, e.rowsBesidePreview(width, preview)...)
	}

	lines = append(lines, "")
	lines = append(lines, text(e.hintText)...)
	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)
	return lines
}

// previewLines is the preview for the highlighted option, or nil when the selector has none or the dialog is too
// narrow to carry one beside a readable option list.
func (e *ExtensionSelectorComponent) previewLines(width int) []string {
	if e.preview == nil || len(e.rowTexts) == 0 {
		return nil
	}
	lines := e.preview(e.cursor)
	if len(lines) == 0 || width-previewCells(lines)-previewGapCells-previewRightCells < previewFloorCells {
		return nil
	}
	return lines
}

// previewCells is the widest preview line in terminal cells.
func previewCells(lines []string) int {
	cells := 0
	for _, line := range lines {
		cells = max(cells, widthx.VisibleWidth(line))
	}
	return cells
}

// rowsBesidePreview renders the option rows with the preview right after the list: the rows take the width of the
// longest row (wrapping only when that would push the preview past the dialog), the preview starts previewGapCells
// past it, top-aligned with the first row, and every line stays at the render width.
func (e *ExtensionSelectorComponent) rowsBesidePreview(width int, preview []string) []string {
	cells := previewCells(preview)
	listMax := width - cells - previewGapCells - previewRightCells
	listWidth := 0
	for _, row := range e.rowTexts {
		listWidth = max(listWidth, 2+widthx.VisibleWidth(row))
	}
	listWidth = min(listWidth, listMax)
	var rows []string
	for _, row := range e.rowTexts {
		rows = append(rows, NewPaddedText(row, 1, 0, nil).Render(listWidth)...)
	}
	gap := strings.Repeat(" ", previewGapCells)
	composed := make([]string, max(len(rows), len(preview)))
	for i := range composed {
		line := strings.Repeat(" ", listWidth)
		if i < len(rows) {
			line = rows[i]
		}
		if i < len(preview) {
			line += gap + preview[i]
		}
		if pad := width - widthx.VisibleWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		composed[i] = line
	}
	rows = composed
	return rows
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
		e.updateList()
	case kb.Matches(data, KBSelectDown) || data == "j":
		e.cursor = min(len(e.options)-1, e.cursor+1)
		e.updateList()
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
	return styledDescriptionLines(ActiveTheme().FgText("text", description), width)
}

// styledDescriptionLines is dialogDescriptionLines for a description whose text color was already applied.
func styledDescriptionLines(styledDescription string, width int) []string {
	if styledDescription == "" {
		return nil
	}
	return append([]string{""}, NewPaddedText(styledDescription, 1, 0, nil).Render(width)...)
}

func countdownTitle(title string, seconds int) string {
	return title + " (" + strconv.Itoa(seconds) + "s)"
}
