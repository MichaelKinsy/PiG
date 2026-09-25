package tui

// MouseRegionHandler handles a mouse event that no nested child handled.
// Mirrors upstream MouseRegionHandler; nil means unhandled.
type MouseRegionHandler func(event TuiMouseEvent) *TuiMouseEventResult

// MouseRegion adds mouse handling to an existing component without changing
// its rendering. Ports pi-tui components/mouse-region.ts.
type MouseRegion struct {
	child   Component
	onMouse MouseRegionHandler
}

// NewMouseRegion wraps child with onMouse. Mirrors upstream new MouseRegion.
func NewMouseRegion(child Component, onMouse MouseRegionHandler) *MouseRegion {
	return &MouseRegion{child: child, onMouse: onMouse}
}

// Render renders the wrapped child unchanged.
func (r *MouseRegion) Render(width int) []string { return r.child.Render(width) }

// HandleMouse gives nested mouse-aware children the first chance, then the
// region's own handler. Mirrors upstream MouseRegion.handleMouse.
func (r *MouseRegion) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if result := DispatchMouseEvent(r.child, event); result != nil {
		return result
	}
	result := r.onMouse(event)
	if result == nil {
		return nil
	}
	return &TuiMouseDispatchResult{TuiMouseEventResult: *result}
}

// Invalidate invalidates the wrapped child.
func (r *MouseRegion) Invalidate() { r.child.Invalidate() }
