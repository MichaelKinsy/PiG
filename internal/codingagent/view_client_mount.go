package codingagent

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
)

// viewMount is one plugin mount as the terminal draws it: a component of a widget container or, in a float slot, of a
// frame over the screen. The interactive loop owns it.
type viewMount struct {
	c       *viewClient
	key     viewMountKey
	slot    *viewSlot
	surface *subprocess.MountSurface
	// ref, intents and digest are those of the last snapshot.
	ref     uiview.Ref
	intents []uiview.IntentDecl
	digest  string
	// accepted reports that the surface holds a view, drawable that the last snapshot's view is the one it holds, live
	// that the mount is live and drawable, hidden that its plugin hid it, and visible that the mount is in the tree: it is
	// live and, in a slot with tabs, the tab that is drawn.
	accepted, drawable, live, hidden, visible bool
	focused                                   bool
	// frame is the frame a mount of a float slot is drawn in, and nil while no dialog holds the keyboard.
	frame viewFloat
	// title is the snapshot's. node is the node of the last focus request, which takes the keys while the focus records
	// name it. asksFocus reports a focus request the client has not answered, and attention that the mount asked to be
	// shown while the user worked in another view.
	title, node          string
	asksFocus, attention bool
	// bindings is the last snapshot's. keys reports that one of them is a key the terminal offers, and hint is the text
	// of the hint row: the keys that have a name, each with its name (see bind).
	bindings []uiview.Binding
	keys     bool
	hint     string
	// cancel ends the intent in flight and is nil when there is none; flight numbers it and cancellable is its
	// declaration's.
	cancel      context.CancelFunc
	flight      uint64
	cancellable bool
	// notOffered holds the intents the terminal told the plugin it does not offer, unbound the bindings it told the
	// plugin it cannot honour, and cutAt the ref of the view it reported as cut.
	notOffered map[string]bool
	unbound    map[string]bool
	cutAt      uiview.Ref

	// lines is what Render returned for lineWidth, lineRows, lineRev, lineTheme and lineHint, and nil when it must draw
	// again.
	lines     []string
	lineWidth int
	lineRows  int
	lineRev   uint64
	lineTheme *tui.Theme
	lineHint  bool

	// dismissed is set when the key that interrupts raised the mount's dismiss intent and cleared when the mount loses
	// the focus. While it is set the key leaves the dialog, so a plugin that does not close its dialog cannot hold the key.
	dismissed bool
}

// focusNode returns the node that takes the keys when the view names none: the node of the last focus request while the
// records still name it, else the first record's.
func (m *viewMount) focusNode(entries []uiview.Focus, request *uiview.Focus) string {
	if request != nil {
		m.node = request.Node
	}
	if !slices.ContainsFunc(entries, func(f uiview.Focus) bool { return f.Node == m.node }) {
		m.node = ""
		if len(entries) > 0 {
			m.node = entries[0].Node
		}
	}
	return m.node
}

// takesFocus reports whether the mount can hold the keyboard focus now: its view has a node that takes keys, or the
// mount binds a key, which makes it focusable as a whole.
func (m *viewMount) takesFocus() bool {
	if m.slot.float {
		// A frame holds the focus as a whole: a view without a node that takes keys still keeps them from what is under it.
		return m.visible && m.frame != nil
	}
	return m.visible && m.slot.focus && (m.keys || m.surface.Focusable())
}

// Render returns the mount's rows at width, cut to the slot's rows, the hint row of its bindings among them while the
// mount holds the focus. It lays the view out again only after the view, the width, the row cap, the theme or the
// presence of the hint row changed.
func (m *viewMount) Render(width int) []string {
	rows := m.slot.rows(m.c.ui.size().rows)
	rev, theme := m.surface.Revision(), tui.ActiveTheme()
	hinted := m.focused && m.hint != ""
	if m.lines != nil && m.lineWidth == width && m.lineRows == rows && m.lineRev == rev && m.lineTheme == theme && m.lineHint == hinted {
		return m.lines
	}
	if hinted {
		// The hint row is one of the slot's rows.
		rows--
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
	if hinted {
		lines = append(lines[:len(lines):len(lines)], viewHintRow(m.hint, width, theme))
		rows++
	}
	if lines == nil {
		lines = []string{}
	}
	m.lines, m.lineWidth, m.lineRows, m.lineRev, m.lineTheme, m.lineHint = lines, width, rows, rev, theme, hinted
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
	m.dismissed = m.dismissed && focused
	m.c.stamp++
	if !focused && m.frame != nil {
		// A dialog lost the keyboard: the client removes the frames unless another dialog has it (settleFloats).
		m.c.left = true
		m.c.wake()
	}
	m.surface.SetFocused(focused)
	switch {
	case focused:
		m.c.focused = m
	case m.c.focused == m:
		m.c.focused = nil
	}
}

// HandleInput takes a key while the mount holds the focus. In a dialog whose plugin declared a dismiss intent the key
// that interrupts dismisses: with no intent in flight it raises that intent with {} and goes no further, so the dialog
// keeps the focus and what closing means is the plugin's. It does so once: pressed again in a dialog that still holds
// the focus, whether the plugin did not close it or has not answered, the key leaves it, so the third press is the
// editor's whatever the plugin does. Otherwise the key leaves the panel or the dialog: it first
// cancels a cancellable intent in flight, then goes to the focused node (a list or an input gives the focus up on it),
// and when the node does not take it the focus returns to the editor all the same. Every other key goes to the focused
// node, and one the node does not take to unhandledKey, which is where the mount's bindings are. Satisfies
// tui.InputHandler.
func (m *viewMount) HandleInput(data string) {
	interrupts := m.c.ui.interrupts(data)
	if interrupts && m.focused && m.cancel == nil && !m.dismissed {
		if d := m.dismissal(); d != nil {
			m.dismissed = true
			m.c.withheld(m, data, string(AppInterrupt))
			m.c.raise(m, d, json.RawMessage(`{}`))
			return
		}
	}
	if interrupts && m.cancel != nil && m.cancellable {
		m.cancel()
	}
	took := m.surface.HandleInput(data)
	switch {
	case interrupts:
		m.c.withheld(m, data, string(AppInterrupt))
		if m.focused {
			m.leave()
		}
	case !took:
		m.c.unhandledKey(m, data)
	}
}

// dismissal returns the intent the plugin declared for the user dismissing the mount, when the mount is one the user
// dismisses: a dialog. A mount of another slot is left, not dismissed.
func (m *viewMount) dismissal() *uiview.IntentDecl {
	if !m.slot.float {
		return nil
	}
	for i := range m.intents {
		if m.intents[i].Dismiss {
			return &m.intents[i]
		}
	}
	return nil
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

// leave returns the focus to the editor: the focused node gave it up, or the user pressed the key that interrupts. A
// dialog's frame goes with the focus.
func (m *viewMount) leave() {
	if !m.focused {
		return
	}
	if m.frame != nil {
		m.frame.Leave()
		m.c.settle()
	} else {
		m.c.ui.focus(nil)
	}
	m.c.ui.render()
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
