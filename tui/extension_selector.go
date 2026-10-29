package tui

// Ports packages/coding-agent/src/modes/interactive/components/extension-selector.ts

import (
	"strconv"
	"strings"
	"time"

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
// list of string options. Like upstream it is a Container: border, spacer,
// title, [spacer, description], spacer, option list, spacer, hint, spacer,
// border.
type ExtensionSelectorComponent struct {
	countdown *CountdownTimer
	Container
	title                 string
	baseTitle             string
	description           string
	options               []string
	cursor                int
	done                  bool
	cancel                bool
	onToggleToolsExpanded func()
	onSelect              func(option string)
	onCancel              func()

	// The themed texts are built when they change, as upstream builds each Text with theme.fg: the title at
	// construction and for a countdown tick, the description at construction, the hint at construction and the
	// rows at construction and when the selection moves (updateList). A theme change leaves them as built; only
	// the borders are drawn with the theme of the moment.
	titleText       *Text
	descriptionText string
	hintText        string
	listContainer   *Container

	// preview draws the right column beside the option rows for the highlighted option, or is nil for upstream's
	// selector, which has no preview column.
	preview func(selectedIndex int) []string
}

// ExtensionSelectorOptions mirrors upstream ExtensionSelectorOptions (extension-selector.ts:12-17).
type ExtensionSelectorOptions struct {
	// TUI receives a render request on every countdown tick.
	TUI TUI
	// Timeout is how long the selector waits before it cancels itself; zero or less disables the countdown, which also needs TUI.
	Timeout time.Duration
	// OnToggleToolsExpanded runs on the app.tools.expand binding.
	OnToggleToolsExpanded func()
	// Description is shown under the title.
	Description string
	// Dispatch runs each countdown second on the loop that owns the component; upstream's interval runs on its event loop. A nil Dispatch runs it on the timer goroutine.
	Dispatch func(func())
}

// NewExtensionSelectorComponent creates a generic selector overlay, as upstream's constructor does (extension-selector.ts:30-85).
// The first option is pre-selected. onSelect receives the confirmed option and onCancel runs when the user cancels or the
// countdown expires; either may be nil, because a host that polls Done and Cancelled needs neither. The host-driven Done,
// Cancelled and SelectedIndex state is set before the callback runs.
func NewExtensionSelectorComponent(title string, options []string, onSelect func(option string), onCancel func(), optionsArg ...ExtensionSelectorOptions) *ExtensionSelectorComponent {
	var opts ExtensionSelectorOptions
	if len(optionsArg) > 0 {
		opts = optionsArg[0]
	}
	e := &ExtensionSelectorComponent{
		title:                 title,
		baseTitle:             title,
		options:               options,
		onToggleToolsExpanded: opts.OnToggleToolsExpanded,
		onSelect:              onSelect,
		onCancel:              onCancel,
		titleText:             NewPaddedText(styledDialogTitle(title), 1, 0, nil),
		listContainer:         NewContainer(),
	}
	if opts.Description != "" {
		e.description = opts.Description
		e.descriptionText = ActiveTheme().Fg("text", opts.Description)
	}
	if opts.Timeout > 0 && opts.TUI != nil {
		// Upstream requests a render on each interval tick, not on the first tick the constructor makes.
		started := false
		e.countdown = NewCountdownTimer(opts.Timeout, opts.Dispatch, func(seconds int) {
			e.SetCountdown(seconds)
			if started {
				opts.TUI.RequestRender()
			}
		}, e.expire)
		started = true
	}
	e.hintText = rawArrowHint() + "  " + extensionActionHint(KBSelectConfirm, "select") + "  " + extensionActionHint(KBSelectCancel, "cancel")
	e.buildChildren()
	e.updateList()
	return e
}

// expire is the countdown's expiry: upstream calls onCancelCallback.
func (e *ExtensionSelectorComponent) expire() {
	if e.done {
		return
	}
	e.Cancel()
	if e.onCancel != nil {
		e.onCancel()
	}
}

// buildChildren adds upstream's constructor children; the description pair exists only when a description is set.
func (e *ExtensionSelectorComponent) buildChildren() {
	e.Clear()
	e.Add(NewDynamicBorder())
	e.Add(NewSpacer(1))
	e.Add(e.titleText)
	if e.descriptionText != "" {
		e.Add(NewSpacer(1))
		e.Add(NewPaddedText(e.descriptionText, 1, 0, nil))
	}
	e.Add(NewSpacer(1))
	e.Add(e.listContainer)
	e.Add(NewSpacer(1))
	e.Add(NewPaddedText(e.hintText, 1, 0, nil))
	e.Add(NewSpacer(1))
	e.Add(NewDynamicBorder())
}

// styledDialogTitle is the title Text content of upstream's selector: theme.fg("accent", theme.bold(title)).
func styledDialogTitle(title string) string {
	return ActiveTheme().Fg("accent", boldText(title))
}

// updateList rebuilds the option rows with the current theme (extension-selector.ts updateList).
func (e *ExtensionSelectorComponent) updateList() {
	e.listContainer.Clear()
	for i := range e.options {
		e.listContainer.Add(NewPaddedText(e.rowText(i), 1, 0, nil))
	}
}

// rowText is the themed text of option i: the arrow marks the selected row.
func (e *ExtensionSelectorComponent) rowText(i int) string {
	t := ActiveTheme()
	if i == e.cursor {
		return t.Fg("accent", "→ ") + t.Fg("accent", e.options[i])
	}
	return "  " + t.Fg("text", e.options[i])
}

// SetPreview supplies the dialog's right column: the lines drawn beside the option rows for the highlighted option,
// which the selector asks for on every render so the column follows the cursor. Nil, the default, draws upstream's
// selector with no preview column. Pig's sprite picker uses it to show the pig of the sprite under the cursor.
// pig divergence (D2): the preview column is Pig's sprite picker's; upstream's selector renders without one.
func (e *ExtensionSelectorComponent) SetPreview(preview func(selectedIndex int) []string) {
	e.preview = preview
	e.Invalidate()
}

// Render is the Container's render, except that with a preview (SetPreview) the option rows take the width of the
// longest row (wrapping only to keep the preview inside the dialog), the preview follows previewGapCells later,
// top-aligned with the first row, and the title, hint and borders keep the full width.
func (e *ExtensionSelectorComponent) Render(width int) []string {
	preview := e.previewLines(width)
	if preview == nil {
		return e.Container.Render(width)
	}
	var lines []string
	for _, child := range e.Children() {
		if child == Component(e.listContainer) {
			lines = append(lines, e.rowsBesidePreview(width, preview)...)
			continue
		}
		lines = append(lines, child.Render(width)...)
	}
	return lines
}

// previewLines is the preview for the highlighted option, or nil when the selector has none or the dialog is too
// narrow to carry one beside a readable option list.
func (e *ExtensionSelectorComponent) previewLines(width int) []string {
	if e.preview == nil || len(e.options) == 0 {
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
	for i := range e.options {
		listWidth = max(listWidth, 2+widthx.VisibleWidth(e.rowText(i)))
	}
	listWidth = min(listWidth, listMax)
	var rows []string
	for i := range e.options {
		rows = append(rows, NewPaddedText(e.rowText(i), 1, 0, nil).Render(listWidth)...)
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
	return composed
}

// SetDescription sets optional explanatory text shown between the title and
// the options. Mirrors upstream ExtensionSelectorOptions.description.
func (e *ExtensionSelectorComponent) SetDescription(description string) {
	e.description = description
	e.descriptionText = ""
	if description != "" {
		e.descriptionText = ActiveTheme().Fg("text", description)
	}
	e.buildChildren()
	e.Invalidate()
}

// SetCountdown shows the seconds left before the selector times out, as
// upstream's countdown sets the title to `${baseTitle} (${s}s)`.
func (e *ExtensionSelectorComponent) SetCountdown(seconds int) {
	e.title = countdownTitle(e.baseTitle, seconds)
	e.titleText.SetText(styledDialogTitle(e.title))
	e.Invalidate()
}

// StartCountdown starts the timer that shows the seconds left in the title and, at zero, cancels the selector (the constructor's opts.timeout branch, which owns a CountdownTimer). dispatch runs each second on the loop that owns the selector; onTick runs after each title update and onExpire after the cancellation. A running countdown is replaced.
func (e *ExtensionSelectorComponent) StartCountdown(timeout time.Duration, dispatch func(func()), onTick, onExpire func()) {
	e.Dispose()
	e.countdown = NewCountdownTimer(timeout, dispatch, func(seconds int) {
		e.SetCountdown(seconds)
		onTick()
	}, func() {
		e.Cancel()
		onExpire()
	})
}

// Dispose stops the countdown (dispose() calls countdown?.dispose()). It is safe to call twice.
func (e *ExtensionSelectorComponent) Dispose() {
	if e.countdown != nil {
		e.countdown.Dispose()
		e.countdown = nil
	}
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
		if selected := e.SelectedValue(); selected != "" {
			e.done = true
			if e.onSelect != nil {
				e.onSelect(selected)
			}
		}
	case kb.Matches(data, KBSelectCancel):
		e.done = true
		e.cancel = true
		if e.onCancel != nil {
			e.onCancel()
		}
	}
	e.Invalidate()
}

func extensionActionHint(action, label string) string {
	return extKeyHint(ActionKeyText(action), label)
}

// rawArrowHint formats the navigation key in dim and its description in muted.
func rawArrowHint() string {
	return extKeyHint("↑↓", "navigate")
}

// extKeyHint formats an already-resolved key in dim and its description in muted, as upstream keyHint does.
func extKeyHint(key, label string) string {
	t := ActiveTheme()
	return t.Fg("dim", key) + t.Fg("muted", " "+label)
}

func countdownTitle(title string, seconds int) string {
	return title + " (" + strconv.Itoa(seconds) + "s)"
}
