package tui

import (
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// cellSizeQuery asks the terminal for its cell size in pixels (CSI 16 t). The
// response is CSI 6 ; height ; width t.
const cellSizeQuery = "\x1b[16t"

var cellSizeResponsePattern = lazyregexp.New(`^\x1b\[6;(\d+);(\d+)t$`)

// Ports packages/tui/src/tui.ts (queryCellSize, cell-size response handling).
// QueryCellSize writes the cell-size query when the terminal supports images,
// since only image rendering uses the cell size. Mirrors upstream tui.ts
// queryCellSize, which start() sends after the terminal starts.
//
// QueryCellSize also records renderer-level continue ownership (continue_signal.go) for standalone callers that do not use ProcessTerminal.EnterRawMode.
func (t *tuiBase) QueryCellSize() {
	t.continues.beginFor(t.terminal)
	if GetCapabilities().Images == "" {
		return
	}
	t.writeOwned(t.out, cellSizeQuery)
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
	heightPx, heightErr := strconv.Atoi(match[1]) //portlint:allow numbers digits-only match; a size past int range (no terminal reports one) is consumed without setting it, where Pi's parseInt would store it
	widthPx, widthErr := strconv.Atoi(match[2])   //portlint:allow numbers same digits-only match and int-range note as the height
	if heightErr != nil || widthErr != nil || heightPx <= 0 || widthPx <= 0 {
		return true
	}
	SetCellDimensions(CellDimensions{WidthPx: widthPx, HeightPx: heightPx})
	t.invalidateMounted()
	t.RequestRender()
	return true
}

// Invalidate is Pi's TUI.invalidate (tui.ts:951-954): every mounted root and every overlay's component, with their
// descendants, so a component that keeps its rows until it is told draws them again after a theme, cell-size or image-support
// change. Its callers are those causes, never a frame: the renderer's own bookkeeping asks for a frame with markDirty.
func (t *tuiBase) Invalidate() {
	t.markDirty()
	if t.frameActive.Load() {
		// A frame holds mu and the base container's lock while its components render, and a render may ask for this (Pi's single
		// thread lets it): the walk, which reads the mounted roots under those locks, runs when the frame ends (unlockFrame), and
		// the next frame draws what the components build then.
		t.invalidateAfterFrame.Store(true)
		return
	}
	t.invalidateMounted()
}

// markDirty flags the renderer's root for the next frame and invalidates no component.
func (t *tuiBase) markDirty() { t.Container.Invalidate() }

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
func (d *altScreenDocument) noteChildrenInvalidated()   { d.base.noteChildrenInvalidated() }

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
	if noter, ok := root.(interface{ noteChildrenInvalidated() }); ok {
		noter.noteChildrenInvalidated()
	}
}

// noteChildrenInvalidated records that the whole-tree walk invalidated the container's children under the active theme; see
// Container.invalidatedTheme.
func (c *Container) noteChildrenInvalidated() {
	c.mu.Lock()
	c.invalidatedTheme = ActiveTheme()
	c.mu.Unlock()
}
