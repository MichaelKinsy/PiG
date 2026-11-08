package components

// Overlays for the web-parity panels (p2-tui-port.md P11): a filterable list with per-row actions, in the look of pi's selectors (borders,
// `→` cursor, dim hints), and a yes/no confirmation. Rows are server data: callers pass text through `LineText`/`SafeText`; the overlay
// sanitizes again.
//
// pi: apps/p2/src/tui/components/list-overlay.ts

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ListRow is a row of a ListOverlay.
type ListRow struct {
	Key   string
	Label string
	// Detail is right-aligned, dim.
	Detail string
	// Description is below the list for the selected row (wrapped to one line here).
	Description string
	// Color is "accent", "muted", "warning", "error" or "success"; "" for none.
	Color string
}

// ListAction is a key of a ListOverlay with a label in its hints.
type ListAction struct {
	// ID is a binding id (`app.session.delete`), matched through the keymap (rebindable).
	ID string
	// Key is, when ID is empty, a fixed key of this panel (`ctrl+t`), matched literally.
	Key   string
	Label string
	Run   func(row *ListRow)
}

// ListOverlayOptions are the options of NewListOverlay.
type ListOverlayOptions struct {
	// Title is read on each render.
	Title   func() string
	Rows    func() []ListRow
	Actions []ListAction
	// OnSelect is the Enter action; nil hides the "open" hint.
	OnSelect func(row ListRow)
	OnClose  func()
	// Height is the number of rows shown; 0 selects 12.
	Height int
	// Footer is lines under the list (status, the selected row's details).
	Footer func() []string
	// Selected preselects this row; nil for none.
	Selected *string
	// Follow selects the last row and keeps following new ones while it stays selected.
	Follow bool
}

// ListOverlay is a filterable list with per-row actions.
type ListOverlay struct {
	focused     bool
	ctx         SelectorContext
	input       *tui.TextInput
	opts        ListOverlayOptions
	height      int
	selected    int
	selectedKey *string
	follow      bool
	followable  bool
}

// NewListOverlay is the ListOverlay constructor.
func NewListOverlay(ctx SelectorContext, options ListOverlayOptions) *ListOverlay {
	height := options.Height
	if height == 0 {
		height = 12
	}
	return &ListOverlay{
		ctx: ctx, input: tui.NewInput(tui.InputOptions{}), opts: options, height: height, selectedKey: options.Selected,
		follow: options.Follow, followable: options.Follow,
	}
}

// SetFocused is the TypeScript's `focused` setter.
func (o *ListOverlay) SetFocused(focused bool) { o.focused = focused }

// Selection is the selected row's key (the preselected one until rows are listed); nil for none.
func (o *ListOverlay) Selection() *string { return o.selectedKey }

// Select selects the row of key (a caller moving the cursor as rows settle, e.g. to the first usable one).
func (o *ListOverlay) Select(key string) {
	o.selectedKey = &key
	o.ctx.RequestRender()
}

func (o *ListOverlay) filtered() []ListRow {
	query := o.input.GetValue()
	rows := o.opts.Rows()
	if query == "" {
		return rows
	}
	return tui.FuzzyFilter(append([]ListRow(nil), rows...), query, func(row ListRow) string { return row.Label + " " + row.Detail })
}

// current is the selected row's index; the selected row follows its key when rows change.
func (o *ListOverlay) current(rows []ListRow) int {
	if o.follow {
		o.selected = len(rows) - 1
	} else if o.selectedKey != nil {
		index := -1
		for i, row := range rows {
			if row.Key == *o.selectedKey {
				index = i
				break
			}
		}
		// A key not listed yet (rows still loading) stays the choice until it is.
		if index < 0 {
			return max(0, min(o.selected, len(rows)-1))
		}
		o.selected = index
	}
	o.selected = max(0, min(o.selected, len(rows)-1))
	o.selectedKey = nil
	if o.selected < len(rows) {
		key := rows[o.selected].Key
		o.selectedKey = &key
	}
	return o.selected
}

// HandleInput is the key handling.
func (o *ListOverlay) HandleInput(data string) {
	keys := o.ctx.Keys()
	rows := o.filtered()
	index := o.current(rows)
	move := func(delta int) {
		if len(rows) == 0 {
			return
		}
		o.selected = ((index+delta)%len(rows) + len(rows)) % len(rows)
		key := rows[o.selected].Key
		o.selectedKey = &key
		o.follow = o.followable && o.selected == len(rows)-1
	}
	switch {
	case keys.Matches(data, "tui.select.up"):
		move(-1)
	case keys.Matches(data, "tui.select.down"):
		move(1)
	case keys.Matches(data, "tui.select.pageUp"):
		move(-min(index, o.height))
	case keys.Matches(data, "tui.select.pageDown"):
		move(min(len(rows)-1-index, o.height))
	case keys.Matches(data, "tui.select.cancel"):
		o.opts.OnClose()
	case keys.Matches(data, "tui.select.confirm"):
		if index < len(rows) && o.opts.OnSelect != nil {
			o.opts.OnSelect(rows[index])
		}
	default:
		var action *ListAction
		for i := range o.opts.Actions {
			candidate := &o.opts.Actions[i]
			if candidate.ID != "" && keys.Matches(data, candidate.ID) || candidate.ID == "" && candidate.Key != "" && tui.MatchesKeyID(data, candidate.Key) {
				action = candidate
				break
			}
		}
		if action != nil {
			var row *ListRow
			if index < len(rows) {
				row = &rows[index]
			}
			action.Run(row)
		} else {
			o.input.HandleInput(data)
			o.selected = 0
			o.selectedKey = nil
		}
	}
	o.ctx.RequestRender()
}

// Render draws the panel.
func (o *ListOverlay) Render(width int) []string {
	theme := o.ctx.Theme().Current()
	border := theme.Fg("border", strings.Repeat("─", max(1, width)))
	rows := o.filtered()
	index := o.current(rows)
	start := max(0, min(index-o.height/2, len(rows)-o.height))
	o.input.SetFocused(o.focused)
	lines := []string{border, widthx.TruncateToWidth(theme.Bold(o.opts.Title()), width, "…", false)}
	lines = append(lines, o.input.Render(width)...)
	lines = append(lines, "")
	for offset, row := range rows[start:min(len(rows), start+o.height)] {
		selected := start+offset == index
		cursor := "  "
		if selected {
			cursor = theme.Fg("accent", "→ ")
		}
		detail := ""
		if row.Detail != "" {
			detail = theme.Fg("dim", render.LineText(row.Detail))
		}
		label := widthx.TruncateToWidth(render.LineText(row.Label), max(8, width-4-widthx.VisibleWidth(detail)), "…", false)
		styled := label
		if row.Color != "" {
			styled = theme.Fg(row.Color, label)
		} else if selected {
			styled = theme.Fg("accent", label)
		}
		left := cursor + styled
		lines = append(lines, widthx.TruncateToWidth(left+strings.Repeat(" ", max(1, width-widthx.VisibleWidth(left)-widthx.VisibleWidth(detail)))+detail, width, "…", false))
	}
	if len(rows) == 0 {
		lines = append(lines, theme.Fg("muted", "  Nothing here"))
	} else if start > 0 || start+o.height < len(rows) {
		lines = append(lines, theme.Fg("muted", fmt.Sprintf("  (%d/%d)", index+1, len(rows))))
	}
	if index < len(rows) && rows[index].Description != "" {
		lines = append(lines, "", widthx.TruncateToWidth(theme.Fg("muted", "  "+render.LineText(rows[index].Description)), width, "…", false))
	}
	// Footer notes wrap (an explanation, a status); at most three lines each.
	if o.opts.Footer != nil {
		for _, line := range o.opts.Footer() {
			wrapped := widthx.WrapTextWithAnsi(line, max(10, width))
			for _, part := range wrapped[:min(3, len(wrapped))] {
				lines = append(lines, widthx.TruncateToWidth(part, width, "…", false))
			}
		}
	}
	var hints []string
	if o.opts.OnSelect != nil {
		hints = append(hints, o.ctx.KeyText("tui.select.confirm")+" open")
	}
	for _, action := range o.opts.Actions {
		keyText := action.Key
		if action.ID != "" {
			keyText = o.ctx.KeyText(action.ID)
			if keyText == "" {
				keyText = action.ID
			}
		}
		hints = append(hints, keyText+" "+action.Label)
	}
	hints = append(hints, o.ctx.KeyText("tui.select.cancel")+" close")
	lines = append(lines, widthx.TruncateToWidth(theme.Fg("dim", "  "+strings.Join(hints, " · ")), width, "…", false), border)
	return lines
}

// Invalidate drops the filter input's caches.
func (o *ListOverlay) Invalidate() { o.input.Invalidate() }

// ConfirmOverlay is a yes/no question drawn by the worker (server actions, not launcher grants): Enter or y confirms.
type ConfirmOverlay struct {
	focused  bool
	ctx      SelectorContext
	lines    []string
	onAnswer func(yes bool)
}

// NewConfirmOverlay is the ConfirmOverlay constructor.
func NewConfirmOverlay(ctx SelectorContext, lines []string, onAnswer func(yes bool)) *ConfirmOverlay {
	return &ConfirmOverlay{ctx: ctx, lines: lines, onAnswer: onAnswer}
}

// SetFocused is the TypeScript's `focused` setter.
func (o *ConfirmOverlay) SetFocused(focused bool) { o.focused = focused }

// HandleInput answers on Enter/y and Escape/n.
func (o *ConfirmOverlay) HandleInput(data string) {
	keys := o.ctx.Keys()
	if keys.Matches(data, "tui.select.confirm") || data == "y" || data == "Y" {
		o.onAnswer(true)
	} else if keys.Matches(data, "tui.select.cancel") || data == "n" || data == "N" {
		o.onAnswer(false)
	}
}

// Render draws the question.
func (o *ConfirmOverlay) Render(width int) []string {
	theme := o.ctx.Theme().Current()
	border := theme.Fg("warning", strings.Repeat("─", max(1, width)))
	out := []string{border}
	for i, line := range o.lines {
		if i == 0 {
			line = theme.Bold(line)
		}
		out = append(out, widthx.TruncateToWidth(line, width, "…", false))
	}
	hint := fmt.Sprintf("  %s/y confirm · %s/n cancel", o.ctx.KeyText("tui.select.confirm"), o.ctx.KeyText("tui.select.cancel"))
	return append(out, widthx.TruncateToWidth(theme.Fg("dim", hint), width, "…", false), border)
}

// Invalidate has nothing to drop.
func (o *ConfirmOverlay) Invalidate() {}

// OverlayHandle is the handle of a shown overlay (TuiOverlayHandle).
type OverlayHandle interface {
	Close()
}

// OverlayHost shows an overlay on pi-tui's stack (TuiContext.overlay); the runtime implements it.
type OverlayHost interface {
	SelectorContext
	Overlay(id string, component tui.Component, options tui.OverlaySpec) OverlayHandle
}

// Confirm shows a confirmation overlay and calls answer with the answer; the TypeScript's `confirm` returns a promise of it.
func Confirm(host OverlayHost, lines []string, answer func(yes bool)) {
	var handle OverlayHandle
	overlay := NewConfirmOverlay(host, lines, func(yes bool) {
		handle.Close()
		answer(yes)
	})
	handle = host.Overlay("confirm", overlay, tui.OverlaySpec{Width: &tui.OverlayValue{Value: 80, Percent: true}, Anchor: "center"})
}

// TextViewerOptions are the options of NewTextViewer.
type TextViewerOptions struct {
	Title string
	// Lines are the lines for a width (Markdown wraps); called on render and with the last width on input.
	Lines   func(width int) []string
	OnClose func()
	// Line is the 1-based line to open at; 0 selects 1.
	Line int
	// Height is the number of lines shown; 0 selects 20.
	Height int
}

// TextViewer is a read-only text panel (file contents, a diff): it scrolls by line and page.
type TextViewer struct {
	focused bool
	ctx     SelectorContext
	opts    TextViewerOptions
	width   int
	offset  int
	rows    int
}

// NewTextViewer is the TextViewer constructor.
func NewTextViewer(ctx SelectorContext, options TextViewerOptions) *TextViewer {
	rows := options.Height
	if rows == 0 {
		rows = 20
	}
	line := options.Line
	if line == 0 {
		line = 1
	}
	return &TextViewer{ctx: ctx, opts: options, width: 80, rows: rows, offset: max(0, line-1-3)}
}

// SetFocused is the TypeScript's `focused` setter.
func (v *TextViewer) SetFocused(focused bool) { v.focused = focused }

// HandleInput scrolls and closes.
func (v *TextViewer) HandleInput(data string) {
	keys := v.ctx.Keys()
	maxOffset := max(0, len(v.opts.Lines(v.width))-v.rows)
	switch {
	case keys.Matches(data, "tui.select.cancel") || keys.Matches(data, "tui.select.confirm"):
		v.opts.OnClose()
	case keys.Matches(data, "tui.select.up"):
		v.offset = max(0, v.offset-1)
	case keys.Matches(data, "tui.select.down"):
		v.offset = min(maxOffset, v.offset+1)
	case keys.Matches(data, "tui.select.pageUp"):
		v.offset = max(0, v.offset-v.rows)
	case keys.Matches(data, "tui.select.pageDown"):
		v.offset = min(maxOffset, v.offset+v.rows)
	}
	v.ctx.RequestRender()
}

// Render draws the panel.
func (v *TextViewer) Render(width int) []string {
	theme := v.ctx.Theme().Current()
	border := theme.Fg("border", strings.Repeat("─", max(1, width)))
	v.width = width
	all := v.opts.Lines(width)
	v.offset = min(v.offset, max(0, len(all)-v.rows))
	visible := all[min(v.offset, len(all)):min(len(all), v.offset+v.rows)]
	out := []string{border, widthx.TruncateToWidth(theme.Bold(render.LineText(v.opts.Title)), width, "…", false)}
	for _, line := range visible {
		out = append(out, widthx.TruncateToWidth(line, width, "…", false))
	}
	hint := fmt.Sprintf("  %d-%d of %d · ↑↓ PgUp/PgDn · %s closes", v.offset+1, v.offset+len(visible), len(all), v.ctx.KeyText("tui.select.cancel"))
	return append(out, widthx.TruncateToWidth(theme.Fg("dim", hint), width, "…", false), border)
}

// Invalidate has nothing to drop.
func (v *TextViewer) Invalidate() {}
