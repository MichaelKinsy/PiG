package codingagent

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D614): the mounts of a slot with tabs (viewSlot.tabs) as tabs. One row lists, in mount order, every
// live mount of the slot and every hidden one the user can open (it declares a whenHidden intent), each by its title, or
// by its key when it has none. Only the view of one tab, the front, is drawn under the row. The row is drawn when it
// lists two entries, a hidden one or a title; a single live mount without a title draws as it does without tabs.

// pig additive (D614): the cells a tab's label is given before the row is too wide, and the fewest it is cut to.
const (
	viewTabLabelCells    = 24
	viewTabLabelMinCells = 4
	viewTabEllipsis      = "..."
	viewTabAttention     = "*"
	viewTabOpenHint      = "enter opens"
)

// viewTabs is the tab row. It holds the keyboard focus while the user stands on an entry whose view takes no key.
type viewTabs struct {
	c       *viewClient
	focused bool
	// lines is what Render returned for lineWidth, lineStamp and lineTheme, and nil when it must draw again.
	lines     []string
	lineWidth int
	lineStamp uint64
	lineTheme *tui.Theme
}

// opener returns the intent a client raises on the mount while it is hidden, or nil.
func (m *viewMount) opener() *uiview.IntentDecl {
	for i := range m.intents {
		if m.intents[i].WhenHidden {
			return &m.intents[i]
		}
	}
	return nil
}

// label is the text of the mount's tab.
func (m *viewMount) label() string {
	if m.title != "" {
		return m.title
	}
	return m.key.key
}

// entries returns the mounts the tab row lists, in mount order.
func (c *viewClient) entries() []*viewMount {
	var entries []*viewMount
	for _, m := range c.order {
		if m.slot.tabs && (m.live || (m.hidden && m.opener() != nil)) {
			entries = append(entries, m)
		}
	}
	return entries
}

// arrange chooses the front tab, decides which mounts are in the tree and whether the tab row is, and lays them out when
// that changed.
func (c *viewClient) arrange() {
	entries := c.entries()
	if old := c.front; old != nil && !old.live {
		// The front tab was hidden: the live tab after it takes its place, else the one before it.
		c.front = nil
		passed := false
		for _, m := range c.order {
			if m == old {
				passed = true
				continue
			}
			if !m.slot.tabs || !m.live {
				continue
			}
			c.front = m
			if passed {
				break
			}
		}
	}
	rowOn := len(entries) > 1
	for _, m := range entries {
		if c.front == nil && m.live {
			c.front = m
		}
		rowOn = rowOn || !m.live || m.title != ""
	}
	drawn := make([]*viewMount, 0, len(c.order))
	for _, m := range c.order {
		visible := m.live && (!m.slot.tabs || m == c.front)
		c.dirty = c.dirty || visible != m.visible
		m.visible = visible
		switch {
		case m.slot.float && visible:
			c.float(m)
		case m.slot.float:
			c.unfloat(m)
		case visible:
			drawn = append(drawn, m)
		}
	}
	if !slices.Equal(drawn, c.drawn) {
		c.drawn = drawn
		c.ui.layout(drawn)
	}
	if rowOn != c.rowOn {
		c.rowOn, c.dirty = rowOn, true
		if c.row == nil {
			c.row = &viewTabs{c: c}
		}
		if rowOn {
			c.ui.tabRow(c.row)
		} else {
			c.ui.tabRow(nil)
		}
	}
	if c.at != nil && (!rowOn || !slices.Contains(entries, c.at)) {
		c.at = nil
	}
}

// stops returns where the focus key stops: the entries of the tab row while it is drawn, else the drawn mounts that
// take the focus.
func (c *viewClient) stops() []*viewMount {
	if c.rowOn {
		return c.entries()
	}
	var panels []*viewMount
	for _, m := range c.drawn {
		if m.takesFocus() {
			panels = append(panels, m)
		}
	}
	return panels
}

// standing returns the stop the keyboard is on: the mount that holds the focus, or the entry the tab row stands on.
func (c *viewClient) standing() *viewMount {
	if c.focused != nil {
		return c.focused
	}
	return c.at
}

// visit moves the keyboard to a stop. Without the tab row the stop is a panel and takes the focus. With it, a live
// entry becomes the front tab and takes the focus when its view takes keys; the tab row holds the focus on any other
// entry, and a hidden one stays hidden until the user confirms it.
func (c *viewClient) visit(m *viewMount) {
	if !c.rowOn {
		c.ui.focus(m)
		return
	}
	c.at = m
	if m.live {
		c.front, m.attention = m, false
	}
	c.dirty = true
	c.settle()
}

// show answers a mount that asked to be shown. Its tab becomes the front one unless the user works elsewhere: the
// keyboard is in another view, on another tab or in an overlay. Then the drawn view stays, and the tab is marked.
func (c *viewClient) show(m *viewMount) {
	switch {
	case !m.slot.tabs || c.front == m:
	case c.front == nil || c.at == m || c.ui.editorFocused():
		c.front, m.attention, c.dirty = m, false, true
	case !m.attention:
		m.attention, c.dirty = true, true
	}
}

// Render returns the row: the front tab bold and underlined, the other live tabs muted, a hidden entry dim and in
// parentheses, the entry the keyboard is on in brackets, and viewTabAttention in the warning colour before a tab that
// asked to be shown. A label is cut by cells, never by bytes.
func (t *viewTabs) Render(width int) []string {
	c, theme := t.c, tui.ActiveTheme()
	if t.lines != nil && t.lineWidth == width && t.lineStamp == c.stamp && t.lineTheme == theme {
		return t.lines
	}
	entries, standing := c.entries(), c.standing()
	// The cells every label is cut to: as many as viewTabLabelCells while the row fits, fewer when it does not.
	limit := viewTabLabelCells
	for ; limit > viewTabLabelMinCells; limit-- {
		cells := 0
		for _, m := range entries {
			cells += 2 + min(widthx.VisibleWidth(m.label()), limit)
			if !m.live || m == standing {
				cells += 2
			}
			if m.attention {
				cells += widthx.VisibleWidth(viewTabAttention)
			}
		}
		if cells-1 <= width {
			break
		}
	}
	var row strings.Builder
	for _, m := range entries {
		row.WriteString(" ")
		if m.attention {
			row.WriteString(theme.Fg("warning", viewTabAttention))
		}
		label := widthx.TruncateToWidth(m.label(), limit, viewTabEllipsis, false)
		if !m.live {
			label = "(" + label + ")"
		}
		if m == standing {
			label = "[" + label + "]"
		}
		switch {
		case !m.live:
			label = theme.Fg("dim", label)
		case m == c.front:
			label = theme.Bold(theme.Underline(label))
		default:
			label = theme.Fg("muted", label)
		}
		row.WriteString(label)
		row.WriteString(" ")
	}
	if c.at != nil && !c.at.live && t.focused {
		row.WriteString(" " + theme.Fg("muted", viewTabOpenHint))
	}
	t.lines = []string{widthx.TruncateToWidth(strings.TrimRight(row.String(), " "), width, viewTabEllipsis, false)}
	t.lineWidth, t.lineStamp, t.lineTheme = width, c.stamp, theme
	return t.lines
}

// Invalidate drops the row.
func (t *viewTabs) Invalidate() { t.lines = nil }

// SetFocused is the renderer telling the row whether it holds the keyboard focus. A row that lost it stands on no
// entry. Satisfies tui.Focusable.
func (t *viewTabs) SetFocused(focused bool) {
	if t.focused == focused {
		return
	}
	t.focused, t.lines = focused, nil
	if !focused {
		t.c.at = nil
	}
}

// HandleInput takes a key while the row holds the focus: the key that interrupts returns the focus to the editor, and
// the key that confirms opens the hidden entry the row stands on by raising its whenHidden intent, whose handler shows
// the mount. Every other key is dropped. Satisfies tui.InputHandler.
func (t *viewTabs) HandleInput(data string) {
	c := t.c
	switch at := c.at; {
	case c.ui.interrupts(data):
		c.ui.focus(nil)
		c.ui.render()
	case at != nil && !at.live && c.ui.confirms(data):
		if d := at.opener(); d != nil {
			c.raise(at, d, json.RawMessage(`{}`))
		}
	}
}
