package tui

// FilterableList: vertical select list with a fuzzy filter input.
//
// The base building block for the session selector and
// the user-message selector. Generic over a string label per item;
// callers wrap their own data outside and look up by index.
//
// Keys:
//
//	↑/↓, Ctrl+P/Ctrl+N         move cursor (filtered view)
//	PageUp / PageDown           jump 10 rows
//	Home / End                  jump to top / bottom
//	Enter                       confirm: SelectedIndex set, Done=true
//	Esc                         cancel: Cancelled=true, Done=true
//	printable / Backspace       edit the filter
//
// The filter is a case-insensitive substring match: the input is
// lower-cased and split on whitespace; an item passes if every token
// is present somewhere in the lower-cased label. Mirrors upstream
// session-selector.ts's `fuzzyMatch` (token AND).

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// SelectListTruncatePrimaryContext describes one primary-column truncation.
// Mirrors upstream SelectListTruncatePrimaryContext.
type SelectListTruncatePrimaryContext struct {
	Text        string
	MaxWidth    int
	ColumnWidth int
	Item        SelectItem
	IsSelected  bool
}

// FilterableList is a modal selector. After Done()==true callers
// inspect SelectedIndex (-1 on cancel) or Cancelled().
type FilterableList struct {
	invalidatable
	Title  string
	Labels []string // display label per item

	// EnableSearch controls whether the upstream-style search input row is
	// rendered and whether printable input edits the filter. Default: true.
	// Settings submenus use false to mirror upstream SelectList-based menus.
	EnableSearch bool

	// Descriptions, when non-nil and width allows, render in a second
	// column to the right of each label. Mirrors upstream
	// select-list.ts SelectItem.description handling. Length should
	// match Labels; missing entries render as empty.
	Descriptions []string

	// MinPrimaryColumnWidth / MaxPrimaryColumnWidth bound the
	// label column when descriptions render. Mirrors upstream
	// SelectListLayoutOptions. Defaults to upstream's 32 cells.
	MinPrimaryColumnWidth int
	MaxPrimaryColumnWidth int

	// TruncatePrimary overrides primary-column truncation. Its output is still
	// clipped to MaxWidth, matching upstream SelectListLayoutOptions.
	TruncatePrimary func(context SelectListTruncatePrimaryContext) string

	// MaxVisible bounds the visible window. Mirrors upstream
	// SelectList.maxVisible. Defaults to 20.
	MaxVisible int

	filter            string
	filtered          []int // indices into Labels passing the current filter
	cursor            int   // position within filtered (NOT Labels)
	scroll            int   // first visible row (within filtered)
	renderedItemCount int
	mousePressedIndex int // -1 when no row press is pending

	done          bool
	cancelled     bool
	selectedIndex int
	onSelect      func(int)
	onCancel      func()
}

// NewFilterableList creates a list with the given labels and an
// optional title shown in the overlay border.
func NewFilterableList(title string, labels []string) *FilterableList {
	f := &FilterableList{
		Title:             title,
		Labels:            labels,
		EnableSearch:      true,
		selectedIndex:     -1,
		mousePressedIndex: -1,
	}
	f.applyFilter()
	return f
}

// Done reports whether the user has confirmed or cancelled.
func (f *FilterableList) Done() bool { return f.done }

// FilterText returns the current filter query typed by the user.
// Used by ShowExtensionEditor to retrieve free-text input.
func (f *FilterableList) FilterText() string { return f.filter }

// CursorIndex returns the current index within the filtered view.
func (f *FilterableList) CursorIndex() int { return f.cursor }

// Cancelled reports whether the user pressed Esc.
func (f *FilterableList) Cancelled() bool { return f.cancelled }

// SelectedIndex returns the chosen index into Labels, or -1 on cancel.
func (f *FilterableList) SelectedIndex() int { return f.selectedIndex }

// SetCursor positions the cursor on a specific filtered-view row.
func (f *FilterableList) SetCursor(idx int) {
	if idx >= 0 && idx < len(f.filtered) {
		f.cursor = idx
		f.fixScroll()
	}
}

// Render draws the optional filter input and SelectList rows, with unpadded muted status rows.
func (f *FilterableList) Render(width int) []string {
	lines := []string{}
	muted := ActiveTheme().Muted
	if muted == "" {
		muted = "\x1b[38;2;128;128;128m"
	}

	if f.EnableSearch {
		// Filter input row: mirrors upstream Input component.
		prompt := "  filter: "
		cursor := "█"
		lines = append(lines, padOrTrunc(prompt+f.filter+cursor, width))
		// Blank line after search input. Mirrors upstream Spacer(1) after Input.
		lines = append(lines, "")
	}

	if len(f.filtered) == 0 {
		f.scroll = 0
		f.renderedItemCount = 0
		lines = append(lines, fg(muted, "  No matching commands"))
		return lines
	}

	// Calculate visible range with scrolling. Mirrors upstream startIndex calc.
	maxRows := f.MaxVisible
	if maxRows <= 0 {
		maxRows = 20
	}
	start := max(0, min(f.cursor-maxRows/2, len(f.filtered)-maxRows))
	end := min(start+maxRows, len(f.filtered))
	f.scroll = start
	f.renderedItemCount = end - start

	// Pre-compute primary column width so description column lines up
	// across visible rows. Mirrors upstream getPrimaryColumnWidth.
	primaryColumnWidth := f.primaryColumnWidth()

	for i := start; i < end; i++ {
		labelIdx := f.filtered[i]
		selected := i == f.cursor
		lines = append(lines, f.renderItem(labelIdx, selected, width, primaryColumnWidth))
	}

	// Scroll indicator when list is larger than the visible window.
	if start > 0 || end < len(f.filtered) {
		scrollText := fmt.Sprintf("  (%d/%d)", f.cursor+1, len(f.filtered))
		lines = append(lines, fg(muted, widthx.TruncateToWidth(scrollText, width-2, "", false)))
	}

	return lines
}

// HandleMouse selects visible rows on press, activates them on click, and moves
// selection one item per wheel report without changing selection on hover.
// Mirrors upstream SelectList.handleMouse.
func (f *FilterableList) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if f.EnableSearch {
		if event.Y == 0 {
			if event.Type == MousePress && event.Button == MouseButtonLeft {
				return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
			}
			return nil
		}
		if event.Y == 1 {
			return nil
		}
	}
	if len(f.filtered) == 0 {
		return nil
	}
	if event.Type == MouseWheel && event.WheelDelta != 0 {
		previous := f.cursor
		if event.WheelDelta < 0 {
			f.cursor--
		} else {
			f.cursor++
		}
		f.cursor = max(0, min(f.cursor, len(f.filtered)-1))
		changed := f.cursor != previous
		if changed {
			f.Invalidate()
		}
		return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Render: new(changed)}}
	}
	if event.Button != MouseButtonLeft || (event.Type != MousePress && event.Type != MouseClick) {
		return nil
	}
	start := f.scroll
	end := min(start+f.renderedItemCount, len(f.filtered))
	rowOffset := 0
	if f.EnableSearch {
		rowOffset = 2
	}
	itemIndex := start + event.Y - rowOffset
	if itemIndex < start || itemIndex >= end {
		return nil
	}
	if event.Type == MousePress {
		f.mousePressedIndex = itemIndex
		f.cursor = itemIndex
		f.Invalidate()
		return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
	}
	clickedIndex := itemIndex
	if f.mousePressedIndex >= 0 {
		clickedIndex = f.mousePressedIndex
	}
	f.mousePressedIndex = -1
	f.cursor = clickedIndex
	f.selectedIndex = f.filtered[f.cursor]
	f.done = true
	f.Invalidate()
	return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true}}
}

// renderItem renders a single row. Mirrors upstream select-list.ts renderItem:
// when a description is present and width allows, label/description render in
// two columns; the selected row uses accent-colored selected text (not a
// background fill) via the selected prefix + selected text theme.
func (f *FilterableList) renderItem(labelIdx int, selected bool, width, primaryColumnWidth int) string {
	desc := f.description(labelIdx)

	const selectedPrefix = "→ "
	const noCursor = "  "
	prefix := noCursor
	if selected {
		prefix = selectedPrefix
	}
	prefixWidth := 2 // both prefixes are 2 cells wide

	const primaryGap = 2
	const minDescWidth = 10
	th := ActiveTheme()
	accent := th.Accent
	if accent == "" {
		accent = "\x1b[38;2;138;190;183m"
	}
	muted := th.Muted
	if muted == "" {
		muted = "\x1b[38;2;128;128;128m"
	}

	if desc != "" && width > 40 {
		effectivePrimary := max(1, min(primaryColumnWidth, width-prefixWidth-4))
		maxPrimaryWidth := max(1, effectivePrimary-primaryGap)
		truncatedLabel := f.truncatePrimary(labelIdx, selected, maxPrimaryWidth, effectivePrimary)
		truncatedLabelWidth := widthx.VisibleWidth(truncatedLabel)
		spacing := strings.Repeat(" ", max(1, effectivePrimary-truncatedLabelWidth))
		descriptionStart := prefixWidth + truncatedLabelWidth + len(spacing)
		remainingWidth := width - descriptionStart - 2

		if remainingWidth > minDescWidth {
			truncatedDesc := widthx.TruncateToWidth(normalizeToSingleLine(desc), remainingWidth, "", false)
			if selected {
				row := prefix + truncatedLabel + spacing + truncatedDesc
				return widthx.TruncateToWidth(accent+row+"\x1b[39m", width, "", false)
			}
			descText := muted + spacing + truncatedDesc + "\x1b[39m"
			row := prefix + truncatedLabel + descText
			return widthx.TruncateToWidth(row, width, "", false)
		}
	}

	// Single-column fallback (no description / narrow width).
	maxWidth := max(width-prefixWidth-2, 1)
	truncatedLabel := f.truncatePrimary(labelIdx, selected, maxWidth, maxWidth)
	row := prefix + truncatedLabel
	if selected {
		return widthx.TruncateToWidth(accent+row+"\x1b[39m", width, "", false)
	}
	return widthx.TruncateToWidth(row, width, "", false)
}

func (f *FilterableList) description(idx int) string {
	if idx < 0 || idx >= len(f.Descriptions) {
		return ""
	}
	return f.Descriptions[idx]
}

func (f *FilterableList) truncatePrimary(labelIdx int, selected bool, maxWidth, columnWidth int) string {
	text := f.Labels[labelIdx]
	truncated := text
	if f.TruncatePrimary != nil {
		truncated = f.TruncatePrimary(SelectListTruncatePrimaryContext{
			Text:        text,
			MaxWidth:    maxWidth,
			ColumnWidth: columnWidth,
			Item: SelectItem{
				Value:       text,
				Label:       text,
				Description: f.description(labelIdx),
			},
			IsSelected: selected,
		})
	}
	return widthx.TruncateToWidth(truncated, maxWidth, "", false)
}

func (f *FilterableList) primaryColumnWidth() int {
	const defaultWidth = 32
	const gap = 2
	rawMin := f.MinPrimaryColumnWidth
	rawMax := f.MaxPrimaryColumnWidth
	switch {
	case rawMin == 0 && rawMax == 0:
		rawMin = defaultWidth
		rawMax = defaultWidth
	case rawMin == 0:
		rawMin = rawMax
	case rawMax == 0:
		rawMax = rawMin
	}
	lo := max(1, min(rawMin, rawMax))
	hi := max(1, max(rawMin, rawMax))

	widest := 0
	for _, idx := range f.filtered {
		w := widthx.VisibleWidth(f.Labels[idx]) + gap
		if w > widest {
			widest = w
		}
	}
	return clampInt(widest, lo, hi)
}

func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }

// normalizeToSingleLine collapses CR/LF runs into a single space and trims.
// Mirrors upstream select-list.ts normalizeToSingleLine.
func normalizeToSingleLine(s string) string {
	out := make([]byte, 0, len(s))
	prevSpace := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' || c == '\n' {
			if !prevSpace {
				out = append(out, ' ')
				prevSpace = true
			}
			continue
		}
		out = append(out, c)
		prevSpace = false
	}
	return strings.TrimSpace(string(out))
}

// HandleInput updates the filter or moves the cursor.
func (f *FilterableList) HandleInput(data string) {
	// Route through the TUI keybinding registry so user overrides in
	// ~/.pig/keybindings.json (tui.select.* / tui.input.*) take effect
	// here. Mirrors upstream filterable-list.ts handleInput dispatch.
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectCancel):
		f.cancelled = true
		f.done = true
		f.selectedIndex = -1
		if f.onCancel != nil {
			f.onCancel()
		}
	case kb.Matches(data, KBSelectConfirm):
		if len(f.filtered) > 0 && f.cursor < len(f.filtered) {
			f.selectedIndex = f.filtered[f.cursor]
			f.done = true
			if f.onSelect != nil {
				f.onSelect(f.selectedIndex)
			}
		}
	case f.EnableSearch && (data == "\x7f" || data == "\b"): // Backspace: filter edit
		if len(f.filter) > 0 {
			f.filter = f.filter[:len(f.filter)-1]
			f.applyFilter()
		}
	case f.EnableSearch && data == "\x15": // Ctrl+U: clear filter (editor-style, not in registry)
		f.filter = ""
		f.applyFilter()
	case kb.Matches(data, KBSelectUp):
		if len(f.filtered) > 0 {
			if f.cursor == 0 {
				f.cursor = len(f.filtered) - 1
				f.fixScroll()
			} else {
				f.moveCursor(-1)
			}
		}
	case kb.Matches(data, KBSelectDown):
		if len(f.filtered) > 0 {
			if f.cursor == len(f.filtered)-1 {
				f.cursor = 0
				f.fixScroll()
			} else {
				f.moveCursor(1)
			}
		}
	case f.EnableSearch && kb.Matches(data, KBSelectPageUp):
		f.moveCursor(-10)
	case f.EnableSearch && kb.Matches(data, KBSelectPageDown):
		f.moveCursor(10)
	case f.EnableSearch && data == "\x1b[H": // Home: search-mode selector extension
		f.cursor = 0
		f.fixScroll()
	case f.EnableSearch && data == "\x1b[F": // End: search-mode selector extension
		f.cursor = max(len(f.filtered)-1, 0)
		f.fixScroll()
	default:
		// Single printable rune (ignore other control sequences).
		if f.EnableSearch && len(data) >= 1 && data[0] >= 0x20 && data[0] != 0x7f {
			// Strip any embedded escape just in case.
			for _, r := range data {
				if r >= 0x20 && r != 0x7f {
					f.filter += string(r)
				}
			}
			f.applyFilter()
		}
	}
	f.Invalidate()
}

func (f *FilterableList) moveCursor(delta int) {
	f.cursor += delta
	if f.cursor < 0 {
		f.cursor = 0
	}
	if f.cursor >= len(f.filtered) {
		f.cursor = len(f.filtered) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
	f.fixScroll()
}

func (f *FilterableList) fixScroll() {
	// Keep cursor visible within a window of ~20 rows. Overlay height
	// caps this in practice; this is a conservative window for scroll
	// math without knowing the actual viewport.
	const window = 20
	if f.cursor < f.scroll {
		f.scroll = f.cursor
	}
	if f.cursor >= f.scroll+window {
		f.scroll = f.cursor - window + 1
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
}

// applyFilter rebuilds f.filtered from scratch given f.filter. Tokens
// are whitespace-split; each must appear (case-insensitive) in the
// label.
func (f *FilterableList) applyFilter() {
	f.filtered = f.filtered[:0]
	tokens := strings.Fields(strings.ToLower(f.filter))
	for i, lbl := range f.Labels {
		ll := strings.ToLower(lbl)
		ok := true
		for _, t := range tokens {
			if !strings.Contains(ll, t) {
				ok = false
				break
			}
		}
		if ok {
			f.filtered = append(f.filtered, i)
		}
	}
	if f.cursor >= len(f.filtered) {
		f.cursor = len(f.filtered) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
	f.fixScroll()
}

func padOrTrunc(s string, w int) string {
	// Column-aware pad/truncate using widthx.VisibleWidth so session labels
	// containing emoji or CJK wide characters display correctly.
	// Preserve ANSI when truncating; stripping it here made long /tree rows lose
	// their role/tool colours and render as plain terminal-default text.
	visible := widthx.VisibleWidth(s)
	if visible > w {
		clipped := widthx.SliceWithWidth(s, 0, w, true)
		if strings.ContainsRune(clipped.Text, 0x1B) {
			clipped.Text += SGRFgReset + SGRBoldDimReset + SGRItalicReset + SGRUnderlineReset
		}
		return clipped.Text + strings.Repeat(" ", max(w-clipped.Width, 0))
	}
	return s + strings.Repeat(" ", w-visible)
}

// dim wraps text in SGR faint (2) and closes with SGR 22 (normal
// intensity), mirroring upstream chalk.dim's `\x1b[2m…\x1b[22m`. Using a
// scoped reset (not `\x1b[0m`) preserves any surrounding fg/bg: e.g. a
// selected row's background: instead of clearing it mid-line.
func dim(s string) string { return "\033[2m" + s + "\033[22m" }

// fg wraps text in a foreground color and closes with SGR 39
// (default foreground), mirroring upstream theme.fg's
// `${ansi}${text}\x1b[39m` ("Reset only foreground color"). A scoped
// reset preserves any surrounding background (e.g. a selected row's
// highlight) that a full `\x1b[0m` would clear mid-line.
func fg(color, s string) string {
	if color == "" {
		return s
	}
	return color + s + SGRFgReset
}
