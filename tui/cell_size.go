package tui

import (
	"fmt"
	"regexp"
	"strconv"
)

// cellSizeQuery asks the terminal for its cell size in pixels (CSI 16 t). The
// response is CSI 6 ; height ; width t.
const cellSizeQuery = "\x1b[16t"

var cellSizeResponsePattern = regexp.MustCompile(`^\x1b\[6;(\d+);(\d+)t$`)

// QueryCellSize writes the cell-size query when the terminal supports images,
// since only image rendering uses the cell size. Mirrors upstream tui.ts
// queryCellSize, which start() sends after the terminal starts.
func (t *tuiBase) QueryCellSize() {
	if GetCapabilities().Images == "" {
		return
	}
	_, _ = fmt.Fprint(t.out, cellSizeQuery)
}

// ConsumeCellSizeResponse reports whether data is a complete cell-size
// response. A response with positive dimensions updates the cell dimensions,
// invalidates every mounted component so images re-render at the new size, and
// requests a render; a response with a zero dimension is still consumed. Any
// other input returns false so it reaches the focused component. Mirrors
// upstream tui.ts consumeCellSizeResponse.
func (t *tuiBase) ConsumeCellSizeResponse(data string) bool {
	match := cellSizeResponsePattern.FindStringSubmatch(data)
	if match == nil {
		return false
	}
	heightPx, heightErr := strconv.Atoi(match[1])
	widthPx, widthErr := strconv.Atoi(match[2])
	if heightErr != nil || widthErr != nil || heightPx <= 0 || widthPx <= 0 {
		return true
	}
	SetCellDimensions(CellDimensions{WidthPx: widthPx, HeightPx: heightPx})
	t.invalidateMounted()
	t.RequestRender()
	return true
}

// invalidateMounted invalidates every mounted root and overlay component with
// their descendants. Mirrors upstream TuiBase.invalidate over getMountedRoots
// and the overlay stack.
func (t *tuiBase) invalidateMounted() {
	roots := []Component{&t.Container}
	if t.mountedRoots != nil {
		roots = t.mountedRoots()
	}
	t.overlayMu.Lock()
	for _, entry := range t.overlayModel.entries {
		roots = append(roots, entry.component)
	}
	t.overlayMu.Unlock()
	for _, root := range roots {
		invalidateComponentTree(root)
	}
}

// childLister is implemented by Container and every type embedding it
// (stacks, scroll views, renderer bases) plus the alt-screen document.
type childLister interface {
	childSnapshot() []Component
}

func (c *Container) childSnapshot() []Component {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Component(nil), c.children...)
}

func (d *altScreenDocument) childSnapshot() []Component { return d.base.childSnapshot() }

// invalidateComponentTree invalidates root and its descendants. Upstream
// Container.invalidate recurses into every child; pig's Container.Invalidate
// marks only the container, because each child keeps its own dirty flag for
// the per-child render cache, so a whole-tree invalidation walks the children.
func invalidateComponentTree(root Component) {
	if root == nil {
		return
	}
	root.Invalidate()
	parent, ok := root.(childLister)
	if !ok {
		return
	}
	for _, child := range parent.childSnapshot() {
		invalidateComponentTree(child)
	}
}
