package codingagent

import (
	"context"
	"strconv"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
)

// viewMount is one plugin mount as the terminal draws it: a component of a widget container. The interactive loop owns
// it.
type viewMount struct {
	c       *viewClient
	key     viewMountKey
	slot    *viewSlot
	surface *subprocess.MountSurface
	// ref, intents and digest are those of the last snapshot.
	ref     uiview.Ref
	intents []uiview.IntentDecl
	digest  string
	// accepted reports that the surface holds a view, drawable that the last snapshot's view is the one it holds, and
	// visible that the mount is live and drawable, so it is in the tree.
	accepted, drawable, visible bool
	focused                     bool
	// cancel ends the intent in flight and is nil when there is none; flight numbers it and cancellable is its
	// declaration's.
	cancel      context.CancelFunc
	flight      uint64
	cancellable bool
	// notOffered holds the intents the terminal told the plugin it does not offer, and cutAt the ref of the view it
	// reported as cut.
	notOffered map[string]bool
	cutAt      uiview.Ref

	// lines is what Render returned for lineWidth, lineRows, lineRev and lineTheme, and nil when it must draw again.
	lines     []string
	lineWidth int
	lineRows  int
	lineRev   uint64
	lineTheme *tui.Theme
}

// takesFocus reports whether the mount can hold the keyboard focus now.
func (m *viewMount) takesFocus() bool {
	return m.visible && m.slot.focus && m.surface.Focusable()
}

// Render returns the mount's rows at width, cut to the slot's rows. It lays the view out again only after the view, the
// width, the row cap or the theme changed.
func (m *viewMount) Render(width int) []string {
	rows := m.slot.rows(m.c.ui.size().rows)
	rev, theme := m.surface.Revision(), tui.ActiveTheme()
	if m.lines != nil && m.lineWidth == width && m.lineRows == rows && m.lineRev == rev && m.lineTheme == theme {
		return m.lines
	}
	lines := m.surface.Render(width)
	if len(lines) > rows {
		measured, limit := int64(len(lines)), int64(rows)
		lines = append(lines[:rows-1:rows-1], " "+theme.Fg("muted", viewCutRow))
		if m.cutAt != m.ref {
			m.cutAt = m.ref
			m.c.report(m, uiview.Notice{
				Code: string(uiview.CodeLimit), Severity: uiview.SeverityInfo, Path: "rows", Measured: &measured, Limit: &limit,
				Message: "the view is " + strconv.FormatInt(measured, 10) + " rows and the slot draws " + strconv.FormatInt(limit, 10),
			})
		}
	}
	if lines == nil {
		lines = []string{}
	}
	m.lines, m.lineWidth, m.lineRows, m.lineRev, m.lineTheme = lines, width, rows, rev, theme
	return lines
}

// Invalidate drops the rows and the components' caches.
func (m *viewMount) Invalidate() {
	m.lines = nil
	m.surface.Invalidate()
}

// repaint is the surface's own change: a key its node took, a loader frame.
func (m *viewMount) repaint() { m.c.ui.render() }

// SetFocused is the renderer telling the mount whether it holds the keyboard focus. Satisfies tui.Focusable.
func (m *viewMount) SetFocused(focused bool) {
	if m.focused == focused {
		return
	}
	m.focused = focused
	m.surface.SetFocused(focused)
	switch {
	case focused:
		m.c.focused = m
	case m.c.focused == m:
		m.c.focused = nil
	}
}

// HandleInput takes a key while the mount holds the focus. The key that interrupts leaves the panel: it first cancels a
// cancellable intent in flight, then goes to the focused node (a list or an input gives the focus up on it), and when
// the node does not take it the focus returns to the editor all the same. Every other key goes to the focused node, and
// one the node does not take to unhandledKey. Satisfies tui.InputHandler.
func (m *viewMount) HandleInput(data string) {
	interrupts := m.c.ui.interrupts(data)
	if interrupts && m.cancel != nil && m.cancellable {
		m.cancel()
	}
	took := m.surface.HandleInput(data)
	switch {
	case interrupts:
		if m.focused {
			m.leave()
		}
	case !took:
		m.c.unhandledKey(m, data)
	}
}

// HandleMouse gives a fullscreen mouse event to the view's components. The mount stays the target of the gesture, and
// only a mount of a slot that takes the focus is focused by it. Satisfies tui.MouseHandler.
func (m *viewMount) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	result := m.surface.HandleMouse(RemoteMouseEvent(event))
	if !result.Handled && !result.Capture && !result.Focus {
		return nil
	}
	dispatched := &tui.TuiMouseDispatchResult{
		Handled: true, Capture: result.Capture, Focus: result.Focus && m.takesFocus(), Render: result.Render,
		Target: tui.TuiMouseDispatchTarget{
			Component: m, OriginX: event.ScreenX - event.X, OriginY: event.ScreenY - event.Y,
			Width: event.Width, Height: event.Height,
		},
	}
	if dispatched.Focus {
		dispatched.FocusTarget = m
	}
	return dispatched
}

// leave returns the focus to the editor: the focused node gave it up, or the user pressed the key that interrupts.
func (m *viewMount) leave() {
	if m.focused {
		m.c.ui.focus(nil)
		m.c.ui.render()
	}
}

// activate raises the first intent declared for the activated node whose argument the node fills. An intent of the node
// the node cannot fill is reported to the plugin once as not offered.
func (m *viewMount) activate(a subprocess.MountActivation) {
	for i := range m.intents {
		d := &m.intents[i]
		if d.NodeKey != a.Node {
			continue
		}
		if arg, ok := intentArgument(d.Arg, a.Value, a.Item); ok {
			m.c.raise(m, d, arg)
			return
		}
		m.reportNotOffered(d.Name, "the terminal cannot fill the argument of intent "+d.Name+" from node "+a.Node)
	}
}

// reportNotOffered tells the plugin, once per intent, that the terminal does not offer it.
func (m *viewMount) reportNotOffered(intent, message string) {
	if m.notOffered[intent] {
		return
	}
	if m.notOffered == nil {
		m.notOffered = map[string]bool{}
	}
	m.notOffered[intent] = true
	m.c.report(m, uiview.Notice{Code: string(uiview.CodeUnsupported), Severity: uiview.SeverityWarning, Path: "intents", Message: message})
}
