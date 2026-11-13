package codingagent

import (
	"slices"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
)

// pig additive (D614): the mounts of a float slot (viewSlot.float, the slot dialog) in the terminal.
//
// A dialog is drawn in a frame over the screen, the renderer's overlay, only while a dialog holds the keyboard. The
// visible dialogs are a stack in the order they were shown: entering draws every one in its frame, the last on top with
// the keyboard, and when the top one ends the one under it has the keyboard again. While no dialog holds the keyboard no
// frame is drawn, so none covers what the user types: one row directly above the editor names the top dialog and the key
// that enters it.
//
// A dialog that is shown enters by the rule of a focus request (viewClient.grant): from a view of the client, from the
// tab row and from an editor that holds no text. Otherwise it waits on the row and the plugin reads an unfocused notice.
// The key that interrupts, or the focus key, leaves the dialogs for the editor; the focus key enters them again.

// viewEnterHint follows the label of the top dialog on the row that stands for the dialogs.
const viewEnterHint = ": F6 to enter"

// float takes m, a visible mount of a float slot, onto the stack, and enters the dialogs when the keyboard may move.
func (c *viewClient) float(m *viewMount) {
	if slices.Contains(c.floats, m) {
		return
	}
	c.floats = append(c.floats, m)
	c.dirty = true
	var why string
	switch {
	case c.focused != nil || c.at != nil:
	case !c.ui.editorFocused():
		why = "another component holds the keyboard focus"
	case !c.ui.editorEmpty():
		why = "the editor holds text"
	}
	if why != "" {
		c.report(m, uiview.Notice{Code: uiview.NoticeUnfocused, Severity: uiview.SeverityInfo, Path: "focus", Message: why})
		return
	}
	c.enter()
}

// unfloat takes m off the stack and removes its frame: the mount was hidden or ended. When it held the keyboard, the
// frame under it, or what held the focus before, has it again.
func (c *viewClient) unfloat(m *viewMount) {
	if !slices.Contains(c.floats, m) {
		return
	}
	c.unframe(m)
	c.floats = slices.DeleteFunc(c.floats, func(o *viewMount) bool { return o == m })
	c.dirty = true
}

// enter draws every dialog of the stack that has no frame in one, in the order they were shown, so the last is on top
// with the keyboard.
func (c *viewClient) enter() {
	for _, m := range c.floats {
		if m.frame == nil {
			m.frame = c.ui.float(m, m.label())
		}
	}
	c.left, c.dirty = false, true
}

// unframe removes the frame of m, when it has one.
func (c *viewClient) unframe(m *viewMount) {
	if m.frame == nil {
		return
	}
	m.frame.Close()
	m.frame = nil
	m.SetFocused(false)
}

// settleFloats removes every frame once no dialog holds the keyboard, and puts the row that stands for the dialogs in
// the tree while they are live and none is drawn.
func (c *viewClient) settleFloats() {
	if c.left {
		c.left = false
		if c.focused == nil || !c.focused.slot.float {
			for _, m := range slices.Backward(c.floats) {
				c.unframe(m)
			}
			c.dirty = true
		}
	}
	waiting := len(c.floats) > 0 && !slices.ContainsFunc(c.floats, func(m *viewMount) bool { return m.frame != nil })
	if waiting == c.waitingOn {
		return
	}
	c.waitingOn, c.dirty = waiting, true
	if c.waiting == nil {
		c.waiting = &viewWaiting{c: c}
	}
	if waiting {
		c.ui.waitRow(c.waiting)
	} else {
		c.ui.waitRow(nil)
	}
}

// viewWaiting is the row that stands for the dialogs while none holds the keyboard: the label of the top one and the
// key that enters it.
type viewWaiting struct {
	c *viewClient
	// lines is what Render returned for lineWidth, lineLabel and lineTheme, and nil when it must draw again.
	lines     []string
	lineWidth int
	lineLabel string
	lineTheme *tui.Theme
}

// Render returns the row, cut by cells, in the theme's muted colour.
func (w *viewWaiting) Render(width int) []string {
	label, theme := "", tui.ActiveTheme()
	if n := len(w.c.floats); n > 0 {
		label = w.c.floats[n-1].label()
	}
	if w.lines != nil && w.lineWidth == width && w.lineLabel == label && w.lineTheme == theme {
		return w.lines
	}
	w.lines = []string{viewHintRow(label+viewEnterHint, width, theme)}
	w.lineWidth, w.lineLabel, w.lineTheme = width, label, theme
	return w.lines
}

// Invalidate drops the row.
func (w *viewWaiting) Invalidate() { w.lines = nil }
