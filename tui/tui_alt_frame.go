package tui

import (
	"slices"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// Pi's TuiAltScreen runs on one thread: a component's render may call any member of the screen that draws it, and the call runs at once, inside the
// frame. The renderer here takes mu for the whole frame and, while it renders the base children, the base container's lock, neither of which is
// reentrant. A subprocess extension's component renders by a callback to the extension, which calls back into the screen from that render; the host runs
// each such call on the goroutine that waits for the callback (the frame's own) through the members of AltScreenFrame, which use what that goroutine
// already holds. Frame says whether the caller is that goroutine.

// AltScreenFrame is the screen as seen from the goroutine that runs a frame, which holds the frame lock (mu) and, while it renders the base children,
// the base container's lock. Its members are those of TuiAltScreen that take one of these locks, without taking it again; the members that take neither
// are called on the screen. The frame goroutine may use it for as long as it runs the frame, and no other goroutine may use it.
type AltScreenFrame struct {
	t *TuiAltScreen
	// baseHeld is set when the goroutine is rendering the base container, so it holds that container's lock.
	baseHeld bool
}

// Frame returns the frame the calling goroutine is running, which holds the screen's frame lock, and false for any other goroutine, including one that
// waits for the frame to end.
func (t *TuiAltScreen) Frame() (AltScreenFrame, bool) {
	if !t.frameActive.Load() {
		return AltScreenFrame{}, false
	}
	me := goroutineid.Current()
	if t.frameOwner.Load() != me {
		return AltScreenFrame{}, false
	}
	return AltScreenFrame{t: t, baseHeld: t.baseOwner.Load() == me}, true
}

// renderBase renders the base container, which holds its own lock across its children's Render: baseOwner tells a child's call back into the renderer
// (Frame) that the goroutine holds it. The goroutine runs a frame, so mu is held as well.
func (t *tuiBase) renderBase(width int, borrowed bool) []string {
	t.baseOwner.Store(goroutineid.Current())
	defer t.baseOwner.Store(0)
	if borrowed {
		return t.Container.renderBorrowed(width)
	}
	return t.Container.Render(width)
}

// GetScreenLines is TuiAltScreen.GetScreenLines: the lines of the last frame that was drawn, not the one being built.
func (f AltScreenFrame) GetScreenLines() []string { return slices.Clone(f.t.previousScreen) }

// HasActiveSelection is TuiAltScreen.HasActiveSelection.
func (f AltScreenFrame) HasActiveSelection() bool {
	_, _, ok := f.t.getSelectionBounds()
	return ok
}

// GetCopyOnSelect is TuiAltScreen.GetCopyOnSelect.
func (f AltScreenFrame) GetCopyOnSelect() bool { return f.t.copyOnSelect }

// SetCopyOnSelect is TuiAltScreen.SetCopyOnSelect.
func (f AltScreenFrame) SetCopyOnSelect(enabled bool) { f.t.copyOnSelect = enabled }

// GetShowHardwareCursor is TuiAltScreen.GetShowHardwareCursor.
func (f AltScreenFrame) GetShowHardwareCursor() bool { return f.t.showHardwareCursor }

// SetShowHardwareCursor is TuiAltScreen.SetShowHardwareCursor.
func (f AltScreenFrame) SetShowHardwareCursor(enabled bool) {
	changed, hasRendered := f.t.setShowHardwareCursorLocked(enabled)
	f.t.showHardwareCursorChanged(enabled, changed, hasRendered)
}

// GetClearOnShrink is TuiAltScreen.GetClearOnShrink.
func (f AltScreenFrame) GetClearOnShrink() bool { return f.t.clearOnShrink }

// SetClearOnShrink is TuiAltScreen.SetClearOnShrink.
func (f AltScreenFrame) SetClearOnShrink(enabled bool) { f.t.clearOnShrink = enabled }

// ResetTextSelection is TuiAltScreen.ResetTextSelection.
func (f AltScreenFrame) ResetTextSelection() {
	f.t.clearTextSelectionLocked()
	f.t.lastClick = nil
}

// SetOnDebug is TuiAltScreen.SetOnDebug.
func (f AltScreenFrame) SetOnDebug(onDebug func()) { f.t.onDebug = onDebug }

// SetLayoutRoot is TuiAltScreen.SetLayoutRoot. The frame in progress keeps the root it began with; the next one uses component.
func (f AltScreenFrame) SetLayoutRoot(component Component) {
	if f.t.setLayoutRootLocked(component) {
		f.t.RequestRender()
	}
}

// SetFocus is TuiAltScreen.SetFocus.
func (f AltScreenFrame) SetFocus(component Component) { f.t.setFocus(component, f.baseHeld) }

// AddChild is TuiAltScreen.AddChild. Pi's render reads its children live, so a child added while the base children render is rendered by the frame in progress.
func (f AltScreenFrame) AddChild(component Component) {
	if f.baseHeld {
		f.t.Container.addLocked(component)
		return
	}
	f.t.Container.Add(component)
}

// RemoveChild is TuiAltScreen.RemoveChild.
func (f AltScreenFrame) RemoveChild(component Component) {
	if f.baseHeld {
		f.t.Container.removeLocked(component)
		return
	}
	f.t.Container.Remove(component)
}

// Clear is TuiAltScreen.Clear.
func (f AltScreenFrame) Clear() {
	if f.baseHeld {
		f.t.Container.clearLocked(true)
		return
	}
	f.t.Container.Clear()
}

// Children is TuiAltScreen.Children.
func (f AltScreenFrame) Children() []Component { return f.t.Container.cloneChildren(f.baseHeld) }

// HandleMouse is TuiAltScreen.HandleMouse.
func (f AltScreenFrame) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	return f.t.Container.handleMouse(event, f.baseHeld)
}

// CancelPendingRender is TuiAltScreen.CancelPendingRender.
func (f AltScreenFrame) CancelPendingRender() { f.t.cancelPendingRenderLocked() }
