package subprocess

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pig additive (D614): the terminal draws a plugin mount with the D107 view kit. A MountSurface is the view surface of
// one mount: the same viewSurface a widget's view uses, so a node is validated, built and drawn by one code path
// whether an extension sent it as a D107 view or mounted it. The surface turns the D107 events of its nodes into
// activations, which the client maps to the mount's intents.

// mountFirstKinds are the kinds of the first D107 set a mount surface draws. The image kind is among them: a mount's
// view carries the bytes of its images in its own `images` list, and the surface keeps them in a store of its own.
var mountFirstKinds = []string{
	ViewKindContainer, ViewKindBox, ViewKindText, ViewKindTruncatedText, ViewKindMarkdown, ViewKindSpacer, ViewKindDynamicBorder,
	ViewKindSelectList, ViewKindSettingsList, ViewKindImage, ViewKindLoader, ViewKindHStack, ViewKindVStack, ViewKindLines,
	ViewKindUserMessage, ViewKindAssistantMessage, ViewKindToolExecution, ViewKindBashExecution, ViewKindDiff,
}

// MountNodeKinds returns the view node kinds a MountSurface draws, sorted: the first D107 set and every registered
// widget kind.
func MountNodeKinds() []string {
	kinds := slices.Concat(mountFirstKinds, slices.Collect(maps.Keys(viewWidgets)))
	slices.Sort(kinds)
	return slices.Compact(kinds)
}

// MountActivation is the user activating a node of a mount's view: an item of a select-list chosen, a value of a
// settings-list changed, an input or editor submitted, a mouse-region clicked.
type MountActivation struct {
	// Node is the id of the activated node.
	Node string
	// Value is the node's value: the chosen item's value, the new value of the setting, the submitted text. It is nil
	// for a node that has none (a mouse-region).
	Value *string
	// Item is the id of the setting whose value changed, and empty otherwise.
	Item string
}

// MountSurfaceOptions are the callbacks of a MountSurface. Each may be nil.
type MountSurfaceOptions struct {
	// Repaint is called after the surface changed on its own: a key its focused node took, a loader frame.
	Repaint func()
	// Activate is called for each activation, on the goroutine that gave the surface the key or the mouse event.
	Activate func(MountActivation)
	// Leave is called when the focused node gave the focus up: a list cancelled, an input escaped.
	Leave func()
	// TerminalRows returns the terminal's height in rows.
	TerminalRows func() int
	// RowBudget returns the rows the mount is drawn with: a taller view is cut by its owner, so a grid draws fewer chip
	// lines to fit. Nil or 0 means no cut.
	RowBudget func() int
	// Images is what the surfaces of one client share to draw their image nodes. Nil: the surface has its own, which
	// transmits no image outside its rows.
	Images *MountImages
}

// MountSurface draws the view of one plugin mount.
type MountSurface struct {
	s        *viewSurface
	activate func(MountActivation)
	leave    func()
}

// NewMountSurface returns a surface with no view. It draws nothing until Accept takes one, and its nodes are drawn
// without the focus until SetFocused gives it.
func NewMountSurface(o MountSurfaceOptions) *MountSurface {
	m := &MountSurface{s: newViewSurface("", newViewImageStore()), activate: o.Activate, leave: o.Leave}
	m.s.repaint = o.Repaint
	m.s.terminalRows = o.TerminalRows
	m.s.rowBudget = o.RowBudget
	m.s.mountImages = o.Images
	if m.s.mountImages == nil {
		m.s.mountImages = &MountImages{}
	}
	m.s.buildMountImage = (*viewBuilder).mountImage
	m.s.unfocused = true
	m.s.sendEvent = m.event
	return m
}

// Accept installs view, a D107 view, as the surface's frame. focus is the id of the node that takes the keys when the
// view names none. An error leaves the frame the surface had; a view equal to the current one is accepted and is not
// built again: when focus names another node than before, that node takes the keys of the frame as it stands.
func (m *MountSurface) Accept(view json.RawMessage, focus string) error {
	s := m.s
	s.mu.Lock()
	moved := s.defaultFocus != focus
	s.defaultFocus = focus
	s.mu.Unlock()
	err := s.accept(view, nil)
	if !errors.Is(err, errViewUnchanged) {
		return err
	}
	if !moved {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.frame; f != nil && f.payload.Focus == "" {
		inst := s.instances[focus]
		if inst != nil && inst.list == nil && inst.settings == nil && !viewWidgetFocusable(inst.kind) {
			inst = nil
		}
		if f.focus != inst {
			f.focus = inst
			s.focusWidgetsLocked(f)
			s.rev++
		}
	}
	return nil
}

// Render returns the lines of the view at width, and none before the first accepted view.
func (m *MountSurface) Render(width int) []string { return m.s.Render(width) }

// Invalidate drops the components' caches.
func (m *MountSurface) Invalidate() { m.s.Invalidate() }

// Revision changes whenever the lines Render returns may have changed for a reason other than the width or the theme.
func (m *MountSurface) Revision() uint64 {
	s := m.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cardChanged.Swap(false) {
		s.rev++
	}
	return s.rev
}

// Focusable reports whether the view has a node that takes keys.
func (m *MountSurface) Focusable() bool {
	s := m.s
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frame != nil && s.frame.focus != nil && !s.closed
}

// SetFocused tells the surface whether it holds the keyboard focus, so its focused node draws its cursor only then.
func (m *MountSurface) SetFocused(focused bool) {
	s := m.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unfocused == !focused {
		return
	}
	s.unfocused = !focused
	s.rev++
	if s.frame != nil {
		s.focusWidgetsLocked(s.frame)
	}
}

// HandleInput gives a key to the focused node and reports whether the node took it.
func (m *MountSurface) HandleInput(data string) bool { return m.s.HandleViewInput(data) }

// HandleMouse gives a mouse event, local to the view's first cell, to the view's components.
func (m *MountSurface) HandleMouse(event extension.RemoteMouseEvent) extension.ViewMouseResult {
	return m.s.HandleViewMouse(event)
}

// Close stops the surface: its loaders stop and its components are disposed.
func (m *MountSurface) Close() {
	m.s.close()
	// The images only this surface's nodes held are freed in the terminal.
	m.s.mountImages.sweep()
}

// event maps a D107 event of a node to an activation or a leave. It runs outside the surface's lock.
func (m *MountSurface) event(ev ViewEventPayload) {
	s := m.s
	s.mu.Lock()
	kind := ""
	if inst := s.instances[ev.Node]; inst != nil {
		kind = inst.kind
	}
	s.mu.Unlock()
	act := MountActivation{Node: ev.Node}
	switch ev.Type {
	case ViewEventSelect:
		value := ""
		if ev.Item != nil {
			value = ev.Item.Value
		}
		act.Value = &value
	case ViewEventChange:
		// An input reports every edit as a change; only a settings-list change is an activation.
		if kind != ViewKindSettingsList {
			return
		}
		value := ev.Value
		act.Value, act.Item = &value, ev.ID
	case ViewEventSubmit:
		value := ev.Value
		act.Value = &value
	case ViewEventMouse:
		if ev.Mouse == nil || ev.Mouse.Type != "click" {
			return
		}
	case ViewEventCancel, ViewEventEscape, ViewEventAbort:
		if m.leave != nil {
			m.leave()
		}
		return
	default:
		return
	}
	if m.activate != nil {
		m.activate(act)
	}
}
