package tui

import "slices"

// Ports the normalized mouse API from pi-tui's tui.ts: TuiMouseEvent,
// TuiMouseEventResult, TuiMouseDispatchTarget, TuiMouseDispatchResult,
// dispatchMouseEvent, retargetMouseEvent, and Container.handleMouse.
//
// Forced Go mechanics: upstream's handleMouse returns either a plain
// TuiMouseEventResult or an already-dispatched TuiMouseDispatchResult (told
// apart by `"target" in result`). Go handlers return *TuiMouseDispatchResult;
// a leaf handler leaves Target.Component nil, which is the Go spelling of
// "no target yet", and DispatchMouseEvent fills it in. The optional wheelDelta
// and clickCount fields are zero when absent: a wheel event always moves at
// least one line and a click counts from one, so zero never carries meaning.

// TuiMouseEventType mirrors upstream TuiMouseEventType.
type TuiMouseEventType string

const (
	MousePress   TuiMouseEventType = "press"
	MouseRelease TuiMouseEventType = "release"
	MouseMove    TuiMouseEventType = "move"
	MouseDrag    TuiMouseEventType = "drag"
	MouseClick   TuiMouseEventType = "click"
	MouseWheel   TuiMouseEventType = "wheel"
)

// TuiMouseButton mirrors upstream TuiMouseButton.
type TuiMouseButton string

const (
	MouseButtonLeft   TuiMouseButton = "left"
	MouseButtonMiddle TuiMouseButton = "middle"
	MouseButtonRight  TuiMouseButton = "right"
	MouseButtonNone   TuiMouseButton = "none"
)

// TuiMouseEvent is a normalized cell-based mouse event. Coordinates are
// zero-based. X/Y are local to the receiving component, ScreenX/ScreenY are
// absolute terminal coordinates, and Width/Height are the receiving
// component's current bounds. Mirrors upstream TuiMouseEvent.
type TuiMouseEvent struct {
	Type    TuiMouseEventType
	Button  TuiMouseButton
	X       int
	Y       int
	ScreenX int
	ScreenY int
	Width   int
	Height  int
	Shift   bool
	Alt     bool
	Ctrl    bool
	// WheelDelta is the logical line delta of a wheel event (negative scrolls
	// up); zero on every other event.
	WheelDelta int
	// ClickCount is the consecutive click count of a click event; zero on every
	// other event.
	ClickCount int
}

// TuiMouseEventResult mirrors upstream TuiMouseEventResult.
type TuiMouseEventResult struct {
	// Handled stops propagation and suppresses renderer-level fallback behavior.
	Handled bool
	// Capture routes subsequent drag/release events to this component. Implies
	// Handled.
	Capture bool
	// Focus gives keyboard focus to this component. Implies Handled.
	Focus bool
	// Render explicitly requests (true) or suppresses (false) a render. nil
	// takes the default: move and release do not render; press, click, drag,
	// and wheel do.
	Render *bool
}

// TuiMouseDispatchTarget records the exact component a mouse event reached and
// the coordinate transform used to reach it. Mirrors upstream
// TuiMouseDispatchTarget.
type TuiMouseDispatchTarget struct {
	Component Component
	OriginX   int
	OriginY   int
	Width     int
	Height    int
}

// TuiMouseDispatchResult is a handled result bound to its target. Mirrors
// upstream TuiMouseDispatchResult. A handler returning a result whose
// Target.Component is nil has not been dispatched yet.
type TuiMouseDispatchResult struct {
	TuiMouseEventResult
	Target TuiMouseDispatchTarget
	// FocusTarget is the keyboard focus target, which may be a delegating
	// parent container. nil means Target.Component.
	FocusTarget Component
}

// MouseHandler is implemented by components with a normalized mouse handler.
// Mirrors upstream Component.handleMouse.
type MouseHandler interface {
	HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult
}

// DispatchMouseEvent dispatches an event to a component and retains the exact
// target and coordinate transform. Containers use it to forward events to
// nested children. Mirrors upstream dispatchMouseEvent.
func DispatchMouseEvent(component Component, event TuiMouseEvent) *TuiMouseDispatchResult {
	handler, ok := component.(MouseHandler)
	if !ok {
		return nil
	}
	result := handler.HandleMouse(event)
	if result == nil {
		return nil
	}
	if result.Target.Component != nil {
		return result
	}
	if !result.Handled && !result.Capture && !result.Focus {
		return nil
	}
	dispatched := *result
	dispatched.Handled = true
	if result.Focus {
		dispatched.FocusTarget = component
	}
	dispatched.Target = TuiMouseDispatchTarget{
		Component: component,
		OriginX:   event.ScreenX - event.X,
		OriginY:   event.ScreenY - event.Y,
		Width:     event.Width,
		Height:    event.Height,
	}
	return &dispatched
}

// RetargetMouseEvent recreates local coordinates for a previously dispatched
// mouse target. Mirrors upstream retargetMouseEvent.
func RetargetMouseEvent(event TuiMouseEvent, target TuiMouseDispatchTarget) TuiMouseEvent {
	event.X = event.ScreenX - target.OriginX
	event.Y = event.ScreenY - target.OriginY
	event.Width = target.Width
	event.Height = target.Height
	return event
}

// mouseChild is one child's rendered height from the last render at a width.
type mouseChild struct {
	component Component
	height    int
}

// mouseLayout caches the child heights of a container's most recent render so
// mouse dispatch does not re-render. Mirrors upstream Container.mouseLayout.
type mouseLayout struct {
	width    int
	children []mouseChild
}

// dispatchToStackedChild forwards an event to the child whose rows contain
// contentY, rebasing the event onto that child. children come from the cached
// layout, or from rendering each child at width when the cache is stale.
func dispatchToStackedChild(children []mouseChild, event TuiMouseEvent, contentX, contentY, width int) *TuiMouseDispatchResult {
	childY := 0
	for _, child := range children {
		if contentY >= childY && contentY < childY+child.height {
			event.X = contentX
			event.Y = contentY - childY
			event.Width = width
			event.Height = child.height
			return DispatchMouseEvent(child.component, event)
		}
		childY += child.height
	}
	return nil
}

func measureMouseChildren(components []Component, width int) []mouseChild {
	children := make([]mouseChild, len(components))
	for i, component := range components {
		children[i] = mouseChild{component: component, height: len(component.Render(width))}
	}
	return children
}

// HandleMouse forwards an event to the child under the pointer. Mirrors
// upstream Container.handleMouse. Upstream also rewrites a focus result's
// focusTarget to the container when a Container subclass handles keyboard
// input; Go embedding has no subclass identity, so a type that embeds
// Container and handles input must override HandleMouse to do the same.
func (c *Container) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if event.Y < 0 || event.Y >= event.Height {
		return nil
	}
	c.mu.RLock()
	layout := c.mouseLayout
	components := append([]Component(nil), c.children...)
	c.mu.RUnlock()
	var children []mouseChild
	if layout != nil && layout.width == event.Width {
		children = layout.children
	} else {
		children = measureMouseChildren(components, event.Width)
	}
	return dispatchToStackedChild(children, event, event.X, event.Y, event.Width)
}

// HandleMouse forwards an event inside the padded content area to the child
// under the pointer. Mirrors upstream Box.handleMouse.
func (b *Box) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	contentWidth := max(1, event.Width-b.PaddingX*2)
	contentY := event.Y - b.PaddingY
	contentX := event.X - b.PaddingX
	if contentY < 0 || contentX < 0 || contentX >= contentWidth {
		return nil
	}
	var children []mouseChild
	if b.mouseLayout != nil && b.mouseLayout.width == contentWidth {
		children = b.mouseLayout.children
	} else {
		children = measureMouseChildren(b.children, contentWidth)
	}
	return dispatchToStackedChild(children, event, contentX, contentY, contentWidth)
}

// isOverlayFocused reports whether the focused component is a visible
// overlay. Mirrors upstream TuiBase.isOverlayFocused.
func (t *tuiBase) isOverlayFocused() bool {
	t.refreshOverlayVisibility(t.width, t.height)
	t.overlayMu.Lock()
	defer t.overlayMu.Unlock()
	focused := t.overlayModel.focusedComponent()
	if focused == nil {
		return false
	}
	for _, entry := range t.overlayModel.entries {
		if entry.visible() && entry.focusComponent() == focused {
			return true
		}
	}
	return false
}

// resolveMouseFocusTarget keeps overlay containers as keyboard focus owners
// when a nested control is clicked. Mirrors upstream
// TuiBase.resolveMouseFocusTarget.
func (t *tuiBase) resolveMouseFocusTarget(component Component) Component {
	t.overlayMu.Lock()
	type candidate struct{ root, focus Component }
	var candidates []candidate
	for _, entry := range slices.Backward(t.overlayModel.entries) {
		if entry.visible() {
			candidates = append(candidates, candidate{entry.component, entry.focusComponent()})
		}
	}
	t.overlayMu.Unlock()
	for _, overlay := range candidates {
		if componentTreeContains(overlay.root, component) {
			return overlay.focus
		}
	}
	return component
}

// overlayMouseDispatch is the result of hit testing the rendered overlays.
type overlayMouseDispatch struct {
	hit    bool
	result *TuiMouseDispatchResult
}

// dispatchMouseToOverlay dispatches to the visually topmost overlay under the
// pointer. Mirrors upstream TuiBase.dispatchMouseToOverlay.
func (t *tuiBase) dispatchMouseToOverlay(event TuiMouseEvent) overlayMouseDispatch {
	t.overlayMu.Lock()
	layouts := t.renderedOverlayLayouts
	t.overlayMu.Unlock()
	for _, layout := range slices.Backward(layouts) {
		bounds := layout.bounds
		if event.ScreenX < bounds.Col || event.ScreenX >= bounds.Col+bounds.Width ||
			event.ScreenY < bounds.Row || event.ScreenY >= bounds.Row+bounds.Height {
			continue
		}
		event.X = event.ScreenX - bounds.Col
		event.Y = event.ScreenY - bounds.Row
		event.Width = bounds.Width
		event.Height = bounds.Height
		result := DispatchMouseEvent(layout.component, event)
		if result != nil && result.Focus {
			focused := *result
			focused.FocusTarget = layout.focus
			result = &focused
		}
		return overlayMouseDispatch{hit: true, result: result}
	}
	return overlayMouseDispatch{}
}
