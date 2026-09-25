package tui

// Ports pi-tui's differential rendering engine and component model.
// Components implement Render(width int) []string and optionally HandleInput(data string).

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ─── Component Interface ──────────────────────────────────────────────────────

// Component is the base interface all TUI widgets implement.
// Render returns a slice of ANSI-annotated lines (no trailing newlines).
// Width is the available terminal columns.
type Component interface {
	Render(width int) []string
	// Invalidate marks the component as needing a redraw on the next tick.
	Invalidate()
}

// InputHandler is implemented by components that want keyboard events.
type InputHandler interface {
	HandleInput(data string)
}

// KeyReleaseReceiver is implemented by components that want Kitty key-release
// events delivered to HandleInput. Mirrors upstream's optional
// Component.wantsKeyRelease (tui.ts:40), which defaults to false. Upstream opts
// in only for games (the space-invaders and doom-overlay example extensions),
// which need key-up to stop movement.
type KeyReleaseReceiver interface {
	WantsKeyRelease() bool
}

// Disposable is implemented by components that need cleanup.
type Disposable interface {
	Dispose()
}

// ─── Invalidation ─────────────────────────────────────────────────────────────

// invalidatable is a mixin that provides Invalidate() + NeedsRedraw().
// dirty is atomic because Invalidate is called from background goroutines
// (e.g. the git-branch watcher marks the StatusLine dirty off the main loop),
// concurrent with main-loop Invalidate calls. The flag is embedded by value in
// pointer-only components, so it is never copied after construction.
type invalidatable struct {
	dirty atomic.Bool
}

func (i *invalidatable) Invalidate()       { i.dirty.Store(true) }
func (i *invalidatable) IsDirty() bool     { return i.dirty.Load() }
func (i *invalidatable) NeedsRedraw() bool { return i.dirty.Swap(false) }

// BaseComponent is the exported equivalent of invalidatable for components
// living in other packages.
type BaseComponent struct{ invalidatable }

// ─── Container ────────────────────────────────────────────────────────────────

// Container stacks child components vertically.
type Container struct {
	invalidatable
	children []Component
	mu       sync.RWMutex
	maxLines atomic.Int64 // 0 = unlimited; set via SetMaxLines

	// childCache memoizes each child's rendered lines keyed by component
	// identity. renderedLines is the immutable concatenation consumed by the
	// package renderers; settled frames reuse it without copying Session history.
	// Render clones it to preserve upstream's fresh-array ownership for callers.
	cacheWidth     int
	cacheTheme     *Theme
	childCache     map[Component]cachedChild
	renderLineHint int
	renderedLines  []string
	renderedValid  bool
	renderVersion  uint64

	// mouseLayout records each child's height from the last uncapped render
	// for mouse dispatch (upstream Container.mouseLayout). nil after a capped
	// render, which does not render every child.
	mouseLayout *mouseLayout
}

type cachedChild struct {
	width   int
	lines   []string
	version uint64
}

func NewContainer(children ...Component) *Container {
	return &Container{children: children}
}

func (c *Container) invalidateStructureLocked() {
	c.renderedValid = false
	c.dirty.Store(true)
}

func (c *Container) Add(comp Component) {
	c.mu.Lock()
	c.children = append(c.children, comp)
	c.invalidateStructureLocked()
	c.mu.Unlock()
}

func (c *Container) Remove(comp Component) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, ch := range c.children {
		if ch == comp {
			c.children = append(c.children[:i], c.children[i+1:]...)
			delete(c.childCache, comp)
			c.invalidateStructureLocked()
			return
		}
	}
}

// Replace swaps oldComp with newComp at the same child index.
// Returns true if oldComp was found.
func (c *Container) Replace(oldComp, newComp Component) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, ch := range c.children {
		if ch == oldComp {
			c.children[i] = newComp
			delete(c.childCache, oldComp)
			c.invalidateStructureLocked()
			return true
		}
	}
	return false
}

// Clear removes every child component. Used by /clear and /new.
func (c *Container) Clear() {
	c.mu.Lock()
	c.children = nil
	c.childCache = nil
	c.invalidateStructureLocked()
	c.mu.Unlock()
}

// SetChildren atomically replaces all children.
func (c *Container) SetChildren(children ...Component) {
	c.mu.Lock()
	c.children = append([]Component(nil), children...)
	c.childCache = nil
	c.invalidateStructureLocked()
	c.mu.Unlock()
}

func (c *Container) LastTwoChildren() (Component, Component) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := len(c.children)
	if n == 0 {
		return nil, nil
	}
	if n == 1 {
		return nil, c.children[0]
	}
	return c.children[n-2], c.children[n-1]
}

func (c *Container) ChildCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.children)
}

// IsEmpty reports whether the container currently has no children.
func (c *Container) IsEmpty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.children) == 0
}

// SetMaxLines caps how many lines Render() returns. When n>0, only the
// last n lines of all children's output are returned. When n==0 (default),
// all lines are returned. Used by the /tree selector to keep the chat
// scrolled to a minimum context window.
// pig-specific: upstream controls the full viewport; this cap is not needed there.
func (c *Container) SetMaxLines(n int) {
	c.maxLines.Store(int64(n))
	c.mu.Lock()
	c.invalidateStructureLocked()
	c.mu.Unlock()
}

// Render returns a fresh slice, matching upstream Container.render's observable
// array ownership. Package renderers use renderBorrowed to read the immutable
// cached concatenation without copying settled transcript history every frame.
func (c *Container) Render(width int) []string {
	return slices.Clone(c.renderBorrowed(width))
}

// renderBorrowed returns immutable lines owned by the container. Callers must
// not edit the slice. A changed child rebuilds the concatenation; an unchanged
// tree at the same width and theme reuses it.
func (c *Container) renderBorrowed(width int) []string {
	lines, _ := c.renderBorrowedVersion(width)
	return lines
}

// renderBorrowedVersion returns the immutable flattened lines and a revision
// that changes whenever those lines are rebuilt. Parent containers use the
// revision to borrow nested container output without trusting a dirty flag that
// an unrelated render may consume.
func (c *Container) renderBorrowedVersion(width int) ([]string, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lines := c.renderBorrowedLocked(width)
	return lines, c.renderVersion
}

func (c *Container) renderBorrowedLocked(width int) []string {
	if c.cacheWidth != width || c.cacheTheme != ActiveTheme() {
		c.childCache = nil
		c.cacheWidth = width
		c.cacheTheme = ActiveTheme()
		c.renderedValid = false
	}
	if c.childCache == nil {
		c.childCache = make(map[Component]cachedChild, len(c.children))
	}

	ml := c.maxLines.Load()
	// pig-specific: when capped (e.g. the /tree context window), render only
	// the trailing children needed to fill maxLines instead of the whole
	// history. Rendering every child and discarding all but the last few
	// lines is O(total entries) per frame, which froze the UI on long
	// sessions and made /tree feel hung when keys were held down.
	if ml > 0 {
		var lines []string
		for _, v := range slices.Backward(c.children) {
			child := c.renderChildLocked(v, width)
			// Prepend into a fresh slice so the cached child slice is never
			// mutated by a later append.
			merged := make([]string, 0, capHint(len(child), len(lines)))
			merged = append(merged, child...)
			lines = append(merged, lines...)
			if int64(len(lines)) >= ml {
				break
			}
		}
		if int64(len(lines)) > ml {
			lines = lines[int64(len(lines))-ml:]
		}
		c.mouseLayout = nil
		c.renderVersion++
		return lines
	}

	if c.renderedValid {
		clean := true
		for _, ch := range c.children {
			if c.childNeedsRenderLocked(ch, width) {
				clean = false
				break
			}
		}
		if clean {
			return c.renderedLines
		}
	}

	lines := make([]string, 0, capHint(c.renderLineHint, 64))
	children := make([]mouseChild, len(c.children))
	for i, ch := range c.children {
		childLines := c.renderChildLocked(ch, width)
		children[i] = mouseChild{component: ch, height: len(childLines)}
		lines = append(lines, childLines...)
	}
	c.mouseLayout = &mouseLayout{width: width, children: children}
	c.renderLineHint = len(lines)
	c.renderedLines = lines
	c.renderedValid = true
	c.renderVersion++
	return c.renderedLines
}

// childNeedsRenderLocked reports whether retrieving ch would call Render.
// c.mu must be held.
func (c *Container) childNeedsRenderLocked(ch Component, width int) bool {
	if nested, ok := ch.(*Container); ok {
		_, version := nested.renderBorrowedVersion(width)
		e, hit := c.childCache[ch]
		return !hit || e.width != width || e.version != version
	}
	dc, ok := ch.(dirtyComponent)
	if !ok || !childCacheable(ch) {
		return true
	}
	e, hit := c.childCache[ch]
	return !hit || e.width != width || dc.IsDirty()
}

// renderChildLocked returns a child's rendered lines, reusing the cached
// result when the child is clean at the same width. Exact nested containers
// publish an immutable revision, so their lines can be borrowed recursively.
// Boxes are never cached because their dirty flag does not recurse into their
// own children. c.mu must be held.
func (c *Container) renderChildLocked(ch Component, width int) []string {
	if nested, ok := ch.(*Container); ok {
		lines, version := nested.renderBorrowedVersion(width)
		c.childCache[ch] = cachedChild{width: width, lines: lines, version: version}
		return lines
	}
	dc, ok := ch.(dirtyComponent)
	if !ok || !childCacheable(ch) {
		return ch.Render(width)
	}
	if e, hit := c.childCache[ch]; hit && e.width == width && !dc.IsDirty() {
		return e.lines
	}
	lines := ch.Render(width)
	// Consume the dirty flag so an unchanged child hits the cache next frame.
	dc.NeedsRedraw()
	c.childCache[ch] = cachedChild{width: width, lines: lines}
	return lines
}

// dirtyComponent is a Component that reports whether its rendered output
// changed since the last consume. Components live-refreshing from the wall
// clock (running bash elapsed, animated loaders) must report IsDirty()==true
// while live so the per-child cache does not freeze them.
type dirtyComponent interface {
	IsDirty() bool
	NeedsRedraw() bool
}

func childCacheable(ch Component) bool {
	_, isBox := ch.(*Box)
	return !isBox
}

// ─── Differential Renderer ────────────────────────────────────────────────────
//
// This renderer ports upstream pi-tui's inline-flow rendering model. The
// key insight: instead of using absolute cursor positioning (which only
// works inside a fixed viewport), we treat the terminal's main buffer
// as a long virtual buffer and use cursor-relative writes. New content
// is emitted with `\r\n`, which scrolls the viewport up and pushes old
// content into the terminal's native scrollback. The user can scroll
// back to see history exactly as if we'd printed normal output, while
// the editor + footer stay glued to the bottom of the viewport.
//
// State variables (in pi-tui terms):
//   prevLines         : last rendered buffer (full content, all lines)
//   cursorRow         : buffer row of "end of content" (= len(prevLines)-1)
//   hardwareCursorRow : buffer row where the actual terminal cursor is
//   prevViewportTop   : buffer row that maps to screen row 0
//   prevWidth/Height  : last terminal dimensions, for change detection
//   maxLinesRendered  : high-water mark, used by clearOnShrink heuristic

// tuiBase is the shared machinery both renderers use: terminal I/O, size, the
// overlay stack, the requestRender coalescing loop, and lifecycle state. It
// mirrors upstream pi-tui's abstract TuiBase (tui.ts). Go has no inheritance, so
// the concrete renderer registers its per-frame paint through the `render` hook
// (upstream's abstract doRender), and both TUI (main screen) and TuiAltScreen
// (fullscreen) embed tuiBase to inherit the base methods by promotion.
type tuiBase struct {
	Container

	// render is the concrete renderer's per-frame paint, set by the
	// constructor. Mirrors upstream's abstract TuiBase.doRender().
	render func()
	// mountedRoots returns the rendered document roots when they differ from
	// the base children. Mirrors upstream's getMountedRoots override.
	mountedRoots func() []Component

	out    io.Writer
	width  int
	height int

	forceRedraw        bool // set by ForceFullRender/requestRender(force); cleared after next frame
	fixedSize          bool // true when constructed via NewWithOutput (tests)
	showHardwareCursor bool // mirrors upstream showHardwareCursor / PI_HARDWARE_CURSOR

	// Overlay state is mutated on the owner loop. The mutex only protects
	// immutable snapshot publication for standalone/test callers that render
	// from another goroutine.
	overlayModel overlayModel
	overlayMu    sync.Mutex
	// renderedOverlayLayouts holds the overlay rectangles of the last composed
	// frame for mouse hit testing and OverlayHandle.GetBounds. Guarded by
	// overlayMu. Mirrors upstream renderedOverlayLayouts.
	renderedOverlayLayouts []renderedOverlayLayout

	// overlayCommandOnOwner schedules transport-neutral overlay commands on the
	// application owner loop. Local built-in calls already run on that loop and
	// use applyOverlayCommand directly.
	overlayCommandOnOwner func(func())

	// kitty graphics support
	kitty bool

	renderRequested  bool
	renderTimer      stoppableTimer
	renderGeneration uint64
	lastRenderAt     time.Time
	now              func() time.Time
	afterFunc        func(time.Duration, func()) stoppableTimer

	// renderOnMain, when set, marshals a scheduled render onto the owner's
	// main loop instead of running doRender on the throttle-timer
	// goroutine. Upstream pi runs its setTimeout render callback on the
	// single JS event loop; the Go port's time.AfterFunc fires on a
	// separate goroutine, so without this hook a scheduled doRender reads
	// the lock-free component tree concurrently with the main loop's
	// mutations (e.g. the working-spinner frame). nil = run inline, which
	// is correct for standalone / single-goroutine use.
	renderOnMain func(render func())

	// tickOnMain, when set, marshals an owned state-machine tick (currently
	// the alt-screen selection auto-scroll) onto the owner's main loop. Its
	// contract differs from renderOnMain: it BLOCKS (backpressures) while the
	// owner loop is alive so a tick is never dropped against a full queue, and
	// returns unenqueued only at owner-loop termination, when the tick is moot.
	// renderOnMain instead drops on a saturated queue even while the loop runs,
	// which would leave the one-shot timer's stale non-nil pointer in place so
	// the tick never re-arms and auto-scroll wedges. Production wires it to a
	// blocking owner-loop post cancelled by the owner-loop context; renderOnMain
	// is deliberately not reused because scheduled renders are cosmetic and
	// droppable. nil = run inline (standalone / single-goroutine use).
	tickOnMain func(func())

	// onWidthChange, when non-nil, is called whenever the terminal width
	// changes between render frames. Used by the extension host to
	// broadcast width_change notifications to subprocess extensions.
	onWidthChange func(width int)

	// onHeightChange mirrors onWidthChange for height. A tmux pane zoom
	// toggle changes height but not width; without this, extensions that
	// render height-dependent content (chain graphs, dashboards) would
	// never know to reflow.
	onHeightChange func(height int)

	mu sync.Mutex // protects render state

	stopped bool // set by Stop(); prevents further Render() calls
}

// TUI is the main-screen renderer: differential rendering into the terminal's
// main screen + scrollback, overlays, cursor, raw input. It ports pi-tui's
// TuiMainScreen (the concrete-type name TUI is kept for pig-wide call-site
// stability; the base machinery lives in the embedded tuiBase).
type TUI struct {
	tuiBase

	prevLines []string
	// Line-reset reuse cache. ApplyLineResets appends the SegmentReset
	// barrier to every line every frame, allocating one string per line.
	// The transcript above the changed region is identical frame-to-frame
	// (typing touches only the editor row; a streaming delta touches only
	// the tail), so resetOutPrev[i] is reused whenever the pre-reset input
	// at index i is unchanged. resetInPrev holds the previous pre-reset
	// input; both are aligned by buffer index.
	resetInPrev    []string
	resetOutPrev   []string
	resetOutWork   []string
	resetCursorRow int
	resetHasCursor bool

	// logDirectory mirrors upstream TuiBase.logDirectory: the directory for
	// the differential-render overflow crash log. Empty falls back to the OS
	// temp directory, as upstream does when it is undefined.
	logDirectory string

	// Inline-flow render state. All values are 0-indexed buffer rows;
	// screen rows are derived as `bufferRow - prevViewportTop`.
	cursorRow         int  // buffer row of the end-of-content marker
	hardwareCursorRow int  // buffer row where the terminal cursor is
	prevViewportTop   int  // buffer row currently shown at screen row 0
	prevWidth         int  // last width seen (for full-redraw detection)
	prevHeight        int  // last height seen
	maxLinesRendered  int  // high-water mark of buffer length
	hasRendered       bool // distinguishes first render from "prev=empty after clear"
	clearOnShrink     bool // mirrors upstream clearOnShrink; settings drive it via SetClearOnShrink

	// previousKittyImageIDs tracks all Kitty image IDs present in the last
	// rendered buffer so we can delete them before a full clear. Mirrors
	// upstream TUI.previousKittyImageIds (Set<number>).
	previousKittyImageIDs map[int]struct{}
}

// OverlayOptions controls the size/position of an overlay.
type OverlayOptions struct {
	// Width, minimum width, and maximum height use terminal-cell units.
	width     overlaySize
	minWidth  int
	maxHeight overlaySize

	anchor           overlayAnchor
	offsetX, offsetY int
	row, col         overlaySize
	margin           overlayMargin
	marginAll        *int

	// visible is evaluated outside the overlay-state lock for local entries.
	visible      func(termWidth, termHeight int) bool
	nonCapturing bool

	// WidthFraction, HeightFraction, and Title configure the private built-in
	// modal wrapper.
	WidthFraction  float64
	HeightFraction float64
	Title          string
}

// OverlayHandle lets callers control an overlay.
type OverlayHandle struct {
	tui *tuiBase
	id  overlayID
}

func (t *tuiBase) refreshOverlayVisibility(width, height int) {
	t.overlayMu.Lock()
	candidates := t.overlayModel.visibilityCandidates()
	t.overlayMu.Unlock()
	if len(candidates) == 0 {
		return
	}
	type visibilityResult struct {
		id      overlayID
		visible bool
	}
	results := make([]visibilityResult, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, visibilityResult{id: candidate.id, visible: candidate.evaluate(width, height)})
	}
	t.overlayMu.Lock()
	for _, result := range results {
		t.overlayModel.apply(overlayCommand{kind: overlaySetEvaluatedVisible, entryID: result.id, visible: result.visible})
	}
	t.overlayMu.Unlock()
}

func (t *tuiBase) currentOverlayVisibility(opts OverlayOptions) bool {
	if opts.visible == nil {
		return true
	}
	return opts.visible(t.width, t.height)
}

func (t *tuiBase) applyOverlayCommand(command overlayCommand) overlayCommandResult {
	t.overlayMu.Lock()
	result := t.overlayModel.apply(command)
	t.overlayMu.Unlock()
	return result
}

// SetOverlayCommandDispatcher installs the ordered ingress used by remote
// producers. The dispatcher owns command ordering on the application loop.
func (t *tuiBase) SetOverlayCommandDispatcher(dispatch func(func())) {
	t.overlayMu.Lock()
	t.overlayCommandOnOwner = dispatch
	t.overlayMu.Unlock()
}

// postOverlayCommand queues an immutable command for owner-loop application.
// It returns false when no owner-loop dispatcher is installed.
func (t *tuiBase) postOverlayCommand(command overlayCommand) bool {
	command.lines = append([]string(nil), command.lines...)
	t.overlayMu.Lock()
	dispatch := t.overlayCommandOnOwner
	t.overlayMu.Unlock()
	if dispatch == nil {
		return false
	}
	dispatch(func() {
		result := t.applyOverlayCommand(command)
		if result.changed {
			t.Invalidate()
		}
	})
	return true
}

// Close permanently removes this overlay.
func (h *OverlayHandle) Close() {
	h.apply(overlayCommand{kind: overlayRemoveTarget, entryID: h.id})
}

// closeTree permanently removes this entry and every nested descendant.
func (h *OverlayHandle) closeTree() {
	h.apply(overlayCommand{kind: overlayParentTeardown, parentID: h.id})
}

// setHidden updates hidden state without unmounting the entry.
func (h *OverlayHandle) setHidden(hidden bool) {
	h.apply(overlayCommand{kind: overlaySetHidden, entryID: h.id, hidden: hidden})
}

func (h *OverlayHandle) apply(command overlayCommand) {
	h.tui.refreshOverlayVisibility(h.tui.width, h.tui.height)
	result := h.tui.applyOverlayCommand(command)
	if result.changed {
		h.tui.Invalidate()
	}
}

// isHidden reports the current mounted entry state. Removed entries are not
// hidden; they no longer exist.
func (h *OverlayHandle) isHidden() bool {
	h.tui.overlayMu.Lock()
	hidden := h.tui.overlayModel.isHidden(h.id)
	h.tui.overlayMu.Unlock()
	return hidden
}

// Focus brings an eligible mounted overlay to the visual front without
// changing append order.
func (h *OverlayHandle) focus() {
	h.apply(overlayCommand{kind: overlayFocus, entryID: h.id})
}

// unfocus releases this overlay to the visual-frontmost visible capturing
// entry. Passing a target requests that mounted visible component explicitly.
func (h *OverlayHandle) unfocus(target ...Component) {
	var component Component
	if len(target) > 0 {
		component = target[0]
	}
	h.apply(overlayCommand{
		kind: overlayUnfocus, entryID: h.id, target: component, explicitTarget: len(target) > 0,
	})
}

// GetBounds returns the most recent rendered bounds of a visible overlay.
// Mirrors upstream OverlayHandle.getBounds.
func (h *OverlayHandle) GetBounds() (OverlayBounds, bool) {
	h.tui.overlayMu.Lock()
	defer h.tui.overlayMu.Unlock()
	entry := h.tui.overlayModel.overlayByID(h.id)
	if entry == nil || !entry.visible() {
		return OverlayBounds{}, false
	}
	for _, layout := range h.tui.renderedOverlayLayouts {
		if layout.id == h.id {
			return layout.bounds, true
		}
	}
	return OverlayBounds{}, false
}

// IsFocused reports whether this overlay currently has focus.
func (h *OverlayHandle) IsFocused() bool { return h.isFocused() }

// isFocused reports strict mounted-entry identity.
func (h *OverlayHandle) isFocused() bool {
	h.tui.overlayMu.Lock()
	focused := h.tui.overlayModel.isFocused(h.id)
	h.tui.overlayMu.Unlock()
	return focused
}

// OpenOverlay pushes a component onto the overlay append stack and returns a
// targeted handle. Callers must Close when the overlay is dismissed.
func (t *tuiBase) OpenOverlay(c Component, opts OverlayOptions) *OverlayHandle {
	if opts.Title != "" || opts.WidthFraction != 0 || opts.HeightFraction != 0 {
		if opts.WidthFraction <= 0 {
			opts.WidthFraction = 0.75
		}
		if opts.HeightFraction <= 0 {
			opts.HeightFraction = 0.75
		}
		c = &modalOverlay{component: c, title: opts.Title}
	}
	result := t.applyOverlayCommand(overlayCommand{kind: overlayMount, component: c, options: opts, visible: t.currentOverlayVisibility(opts)})
	if !result.changed {
		return nil
	}
	t.Invalidate()
	return &OverlayHandle{tui: t, id: result.entryID}
}

func (t *tuiBase) openOverlayWithOptionsFactory(component Component, factory func() OverlayOptions) *OverlayHandle {
	if factory == nil {
		return t.OpenOverlay(component, OverlayOptions{})
	}
	return t.OpenOverlay(component, factory())
}

// openModalOverlay preserves Pig's built-in selector shell while generic
// overlays remain component-framed.
func (t *tuiBase) openModalOverlay(component Component, title string, widthFraction, heightFraction float64) *OverlayHandle {
	if widthFraction <= 0 {
		widthFraction = 0.75
	}
	if heightFraction <= 0 {
		heightFraction = 0.75
	}
	return t.OpenOverlay(component, OverlayOptions{
		Title:          title,
		WidthFraction:  widthFraction,
		HeightFraction: heightFraction,
	})
}

func (t *tuiBase) composeOverlayLines(background []string, width, height int) []string {
	lines, _ := t.composeOverlayLinesWithStats(background, width, height)
	return lines
}

func (t *tuiBase) composeOverlayLinesWithStats(background []string, width, height int) ([]string, overlayCompositionStats) {
	t.applyOverlayCommand(overlayCommand{kind: overlayGeometryChanged, width: width, height: height})
	t.refreshOverlayVisibility(width, height)
	t.overlayMu.Lock()
	snapshot := t.overlayModel.snapshot()
	t.overlayMu.Unlock()
	lines, stats := composeOverlaySnapshotWithStats(background, snapshot, width, height)
	t.overlayMu.Lock()
	t.renderedOverlayLayouts = stats.Layouts
	t.overlayMu.Unlock()
	return lines, stats
}

func (t *tuiBase) openNestedOverlay(parent *OverlayHandle, component Component, opts OverlayOptions) *OverlayHandle {
	if parent == nil || parent.tui != t {
		return nil
	}
	result := t.applyOverlayCommand(overlayCommand{
		kind: overlayNestedMount, parentID: parent.id, component: component, options: opts,
		visible: t.currentOverlayVisibility(opts),
	})
	if !result.changed {
		return nil
	}
	t.Invalidate()
	return &OverlayHandle{tui: t, id: result.entryID}
}

func (t *tuiBase) updateOverlayGeometry(width, height int) uint64 {
	result := t.applyOverlayCommand(overlayCommand{kind: overlayGeometryChanged, width: width, height: height})
	return result.geometryGeneration
}

func (t *tuiBase) replaceOverlaySnapshot(handle *OverlayHandle, geometryGeneration, sequence uint64, lines []string, visible bool) bool {
	if handle == nil || handle.tui != t {
		return false
	}
	result := t.applyOverlayCommand(overlayCommand{
		kind: overlayReplaceSnapshot, entryID: handle.id,
		geometryGeneration: geometryGeneration, frameSequence: sequence,
		lines: lines, visible: visible,
	})
	if result.changed {
		t.Invalidate()
	}
	return result.changed
}

func (t *tuiBase) overlaySnapshot() overlayStateSnapshot {
	t.overlayMu.Lock()
	snapshot := t.overlayModel.snapshot()
	t.overlayMu.Unlock()
	return snapshot
}

// hideOverlay permanently removes the most recently appended mounted entry.
// Focus and visual order do not affect this target.
func (t *tuiBase) hideOverlay() {
	t.refreshOverlayVisibility(t.width, t.height)
	result := t.applyOverlayCommand(overlayCommand{kind: overlayRemoveAppendTail})
	if result.changed {
		t.Invalidate()
	}
}

// SetFocus records a non-overlay target for focus restoration.
func (t *tuiBase) SetFocus(component Component) {
	previous := t.FocusedComponent()
	t.applyOverlayCommand(overlayCommand{
		kind: overlaySetFocusTarget, target: component,
		previousFocusMounted: t.componentMounted(previous),
	})
}

func (t *tuiBase) componentMounted(target Component) bool {
	if target == nil {
		return false
	}
	t.overlayMu.Lock()
	overlayID := t.overlayModel.mountedComponentID(target)
	t.overlayMu.Unlock()
	if overlayID != 0 {
		return true
	}
	t.Container.mu.RLock()
	roots := append([]Component(nil), t.children...)
	t.Container.mu.RUnlock()
	for _, root := range roots {
		if componentTreeContains(root, target) {
			return true
		}
	}
	return false
}

func componentTreeContains(root, target Component) bool {
	if root == target {
		return true
	}
	var children []Component
	var container *Container
	switch component := root.(type) {
	case *Container:
		container = component
	case *Stack:
		container = component.Container
	case *HStack:
		container = component.Container
	case *VStack:
		container = component.Container
	case *ScrollView:
		container = component.Container
	case *Box:
		children = component.children
	}
	if container != nil {
		container.mu.RLock()
		children = append(children, container.children...)
		container.mu.RUnlock()
	}
	for _, child := range children {
		if componentTreeContains(child, target) {
			return true
		}
	}
	return false
}

// FocusedComponent returns the current overlay or non-overlay focus target.
func (t *tuiBase) FocusedComponent() Component {
	t.overlayMu.Lock()
	component := t.overlayModel.focusedComponent()
	t.overlayMu.Unlock()
	return component
}

// ActiveOverlay returns the currently focused eligible overlay component.
func (t *tuiBase) ActiveOverlay() Component {
	t.refreshOverlayVisibility(t.width, t.height)
	focused := t.FocusedComponent()
	blockerMounted := t.componentMounted(focused)
	t.overlayMu.Lock()
	t.overlayModel.prepareInput(blockerMounted)
	input := t.overlayModel.inputSnapshot()
	t.overlayMu.Unlock()
	if !input.eligible {
		return nil
	}
	return input.component
}

// hasOverlay reports whether any overlay is visible. Mirrors upstream
// hasOverlay; HasOverlay below intentionally reports mounted entries for the
// renderer-switch guard.
func (t *tuiBase) hasOverlay() bool {
	t.refreshOverlayVisibility(t.width, t.height)
	t.overlayMu.Lock()
	visible := t.overlayModel.visibleAny()
	t.overlayMu.Unlock()
	return visible
}

// HasOverlay reports whether any overlay entry is mounted. The interactive
// driver uses this for the upstream hasOverlayEntries renderer-switch guard.
func (t *tuiBase) HasOverlay() bool {
	t.overlayMu.Lock()
	mounted := t.overlayModel.mountedAny()
	t.overlayMu.Unlock()
	return mounted
}

type stoppableTimer interface {
	Stop() bool
}

const minRenderInterval = 16 * time.Millisecond

// New creates and initialises a TUI instance.
func New() *TUI {
	t := &TUI{
		tuiBase: tuiBase{
			out:                os.Stdout,
			showHardwareCursor: os.Getenv("PI_HARDWARE_CURSOR") == "1",
			now:                time.Now,
			afterFunc: func(d time.Duration, fn func()) stoppableTimer {
				return time.AfterFunc(d, fn)
			},
		},
	}
	t.render = t.doRender
	t.updateSize()
	t.kitty = detectKitty()
	return t
}

// NewWithOutput creates a TUI that writes to a fixed io.Writer with fixed
// dimensions. Used only by tests; production code uses New().
func NewWithOutput(out io.Writer, cols, rows int) *TUI {
	t := &TUI{
		tuiBase: tuiBase{
			out:                out,
			width:              cols,
			height:             rows,
			fixedSize:          true,
			showHardwareCursor: os.Getenv("PI_HARDWARE_CURSOR") == "1",
			now:                time.Now,
			afterFunc: func(d time.Duration, fn func()) stoppableTimer {
				return time.AfterFunc(d, fn)
			},
		},
	}
	t.render = t.doRender
	return t
}

// GetShowHardwareCursor reports whether the real terminal cursor is shown.
// Mirrors upstream TUI.getShowHardwareCursor().
func (t *tuiBase) GetShowHardwareCursor() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.showHardwareCursor
}

// SetShowHardwareCursor controls whether the real terminal cursor is shown
// while still positioning it for IME/caret parity. Mirrors upstream
// TUI.setShowHardwareCursor().
func (t *TUI) SetShowHardwareCursor(enabled bool) {
	t.mu.Lock()
	if t.showHardwareCursor == enabled {
		t.mu.Unlock()
		return
	}
	t.showHardwareCursor = enabled
	hasRendered := t.hasRendered
	t.mu.Unlock()
	if !enabled {
		t.HideCursor()
	}
	if hasRendered {
		t.Render()
	}
}

// GetClearOnShrink reports whether shrinking content triggers a full redraw
// to clear empty rows. Mirrors upstream TUI.getClearOnShrink().
func (t *TUI) GetClearOnShrink() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.clearOnShrink
}

// SetClearOnShrink configures whether shrinking content triggers a full
// redraw when no overlays are active. Mirrors upstream
// TUI.setClearOnShrink().
func (t *TUI) SetClearOnShrink(enabled bool) {
	t.mu.Lock()
	t.clearOnShrink = enabled
	t.mu.Unlock()
}

func (t *tuiBase) updateSize() {
	if t.fixedSize {
		return
	}
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}
	t.width = w
	t.height = h
}

// SetFixedSize changes the size of a renderer built with a fixed size
// (NewWithOutput), as a terminal resize would; the next render sees it.
// Renderers that read the real terminal size ignore it.
func (t *tuiBase) SetFixedSize(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fixedSize {
		t.width, t.height = cols, rows
	}
}

// Width returns the current terminal width.
func (t *tuiBase) Width() int { return t.width }

// RenderSnapshot returns the current frame's fully rendered lines at the
// given width without writing to the terminal. Used by the /debug command
// to dump the frame. Mirrors upstream TUI.render(width) (tui.ts).
func (t *TUI) RenderSnapshot(width int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Container.Render(width)
}

// SetOnWidthChange registers a callback that fires whenever the terminal
// width changes between render frames. Thread-safe.
func (t *tuiBase) SetOnWidthChange(fn func(width int)) {
	t.mu.Lock()
	t.onWidthChange = fn
	t.mu.Unlock()
}

// SetOnHeightChange registers a callback that fires whenever the terminal
// height changes between render frames. Thread-safe.
func (t *tuiBase) SetOnHeightChange(fn func(height int)) {
	t.mu.Lock()
	t.onHeightChange = fn
	t.mu.Unlock()
}

// SetRenderDispatcher installs a hook that runs throttled scheduled renders
// on the caller's main loop. The dispatcher receives a render closure and is
// responsible for eventually invoking it on the goroutine that owns
// component-tree mutation (it may enqueue it on an event loop). When nil
// (default), scheduled renders run inline on the throttle-timer goroutine,
// which is correct for standalone / single-goroutine use. Thread-safe.
func (t *tuiBase) SetRenderDispatcher(dispatch func(render func())) {
	t.mu.Lock()
	t.renderOnMain = dispatch
	t.mu.Unlock()
}

// SetTickDispatcher installs the blocking owner-loop seam for owned
// state-machine ticks (alt-screen selection auto-scroll). It must marshal fn
// onto the loop that owns rendering, backpressuring rather than dropping while
// that loop is alive; see the tickOnMain doc. When unset the tick runs inline.
// Thread-safe.
func (t *tuiBase) SetTickDispatcher(dispatch func(func())) {
	t.mu.Lock()
	t.tickOnMain = dispatch
	t.mu.Unlock()
}

// Height returns the current terminal height.
func (t *tuiBase) Height() int { return t.height }

// ForceFullRender marks the next Render() as a destructive full repaint.
// Use it when the physical buffer is invalid, including semantic transcript
// replacement, not for ordinary dynamic shrink or streaming updates.
func (t *TUI) ForceFullRender() {
	if t.stopped {
		return
	}
	t.mu.Lock()
	t.forceRedraw = true
	t.mu.Unlock()
}

// RequestRender asks the TUI to render soon, coalescing repeated calls and
// enforcing the upstream 16ms frame throttle. Use this for hot streaming paths
// (thinking/text deltas); direct Render() is reserved for low-frequency state
// changes and final flushes.
func (t *tuiBase) RequestRender() {
	if t.stopped {
		return
	}
	t.requestRender(false)
}

// requestRender mirrors upstream TUI.requestRender(): coalesce repeated
// render requests and enforce a minimum delay between frames.
func (t *tuiBase) requestRender(force bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if force {
		t.forceRedraw = true
		t.renderGeneration++
		if t.renderTimer != nil {
			t.renderTimer.Stop()
			t.renderTimer = nil
		}
		t.renderRequested = true
		generation := t.renderGeneration
		t.renderTimer = t.afterFunc(0, func() { t.runScheduledRender(generation) })
		return
	}
	if t.renderRequested {
		return
	}
	t.renderRequested = true
	t.scheduleRenderLocked()
}

func (t *tuiBase) scheduleRenderLocked() {
	if t.renderTimer != nil || !t.renderRequested {
		return
	}
	delay := time.Duration(0)
	if !t.lastRenderAt.IsZero() {
		elapsed := t.now().Sub(t.lastRenderAt)
		if elapsed < minRenderInterval {
			delay = minRenderInterval - elapsed
		}
	}
	generation := t.renderGeneration
	t.renderTimer = t.afterFunc(delay, func() { t.runScheduledRender(generation) })
}

func (t *tuiBase) runScheduledRender(generation uint64) {
	t.mu.Lock()
	if generation != t.renderGeneration {
		t.mu.Unlock()
		return
	}
	t.renderTimer = nil
	if !t.renderRequested {
		t.mu.Unlock()
		return
	}
	t.renderRequested = false
	dispatch := t.renderOnMain
	t.mu.Unlock()

	// The throttle delay ran on a timer goroutine, but the render itself
	// must run on the owner's main loop so doRender never reads the
	// lock-free component tree concurrently with the main loop's mutations.
	// Mirrors upstream's single-event-loop setTimeout render callback.
	if dispatch != nil {
		dispatch(func() { t.renderScheduled(generation) })
		return
	}
	t.renderScheduled(generation)
}

func (t *tuiBase) renderScheduled(generation uint64) {
	t.mu.Lock()
	current := generation == t.renderGeneration
	t.mu.Unlock()
	if !current {
		return
	}
	t.renderAndReschedule()
}

// renderAndReschedule performs the actual render and schedules a follow-up
// frame if more render requests arrived. It must run on whichever goroutine
// owns component-tree mutation (the main loop when renderOnMain is set).
func (t *tuiBase) renderAndReschedule() {
	t.render()

	t.mu.Lock()
	t.scheduleRenderLocked()
	t.mu.Unlock()
}

// CancelPendingRender invalidates a throttled frame, including one whose timer
// callback has already handed it to the owner loop. It maps the single-threaded
// JavaScript event-loop rule that a later state transition can consume a queued
// request before its callback runs.
func (t *tuiBase) CancelPendingRender() {
	t.mu.Lock()
	t.renderGeneration++
	t.renderRequested = false
	if t.renderTimer != nil {
		t.renderTimer.Stop()
		t.renderTimer = nil
	}
	t.mu.Unlock()
}

// Render performs a differential render pass using inline-flow output.
//
// Mirrors upstream pi-tui's `render()`. New content is appended with
// `\r\n`, which scrolls the viewport up and pushes older lines into
// the terminal's native scrollback. The terminal's normal scrolling
// makes our chat history reachable via the user's mouse wheel / Cmd-↑
// after pig exits, just like a regular shell.
//
// Algorithm sketch:
//  1. Detect width/height change → fullRender(clear).
//  2. First render → fullRender(no clear). Lines emitted with `\r\n`
//     between them flow naturally.
//  3. Diff prevLines vs newLines to find [firstChanged, lastChanged].
//  4. If visible tail content shrinks and exposes rows above the old viewport,
//     repaint only the new visible viewport.
//  5. If only deletions remain, clear those rows in place.
//  6. If firstChanged is above the current viewport → fullRender(clear).
//  7. Otherwise: move cursor to firstChanged (scrolling if it's below
//     the viewport bottom), rewrite affected lines, clear any extras.
func (t *tuiBase) Render() {
	if t.stopped {
		return
	}
	t.CancelPendingRender()
	t.render()
}

func findCursorPosition(lines []string, height int) (widthx.CursorPosition, int, string, bool) {
	if len(lines) == 0 || height <= 0 {
		return widthx.CursorPosition{}, -1, "", false
	}
	viewportTop := max(len(lines)-height, 0)
	for row := len(lines) - 1; row >= viewportTop; row-- {
		before, after, ok := strings.Cut(lines[row], widthx.CursorMarker)
		if ok {
			return widthx.CursorPosition{Row: row, Col: widthx.VisibleWidth(before)}, row, before + after, true
		}
	}
	return widthx.CursorPosition{}, -1, "", false
}

func (t *TUI) applyLineResetsCached(lines []string) []string {
	return t.applyLineResetsCachedWithCursor(lines, -1, "")
}

func (t *TUI) applyLineResetsCachedWithCursor(lines []string, cursorRow int, cursorFreeLine string) []string {
	if len(lines) == 0 {
		t.resetInPrev = nil
		t.resetOutPrev = nil
		t.resetOutWork = nil
		t.resetHasCursor = false
		return lines
	}
	if cap(t.resetOutWork) < len(lines) {
		t.resetOutWork = make([]string, len(lines))
	} else {
		t.resetOutWork = t.resetOutWork[:len(lines)]
	}
	out := t.resetOutWork
	for i, line := range lines {
		wasCursorRow := t.resetHasCursor && i == t.resetCursorRow
		isCursorRow := cursorRow >= 0 && i == cursorRow
		if !wasCursorRow && !isCursorRow && i < len(t.resetInPrev) && t.resetInPrev[i] == line {
			out[i] = t.resetOutPrev[i]
			continue
		}
		if isCursorRow {
			line = cursorFreeLine
		}
		out[i] = widthx.ApplyLineReset(line)
	}
	previousOut := t.resetOutPrev
	t.resetInPrev = lines
	t.resetOutPrev = out
	t.resetOutWork = previousOut
	t.resetCursorRow = cursorRow
	t.resetHasCursor = cursorRow >= 0
	return out
}

func (t *TUI) doRender() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRenderAt = t.now()

	t.updateSize()

	// Collect lines from the component tree, then compose from an immutable
	// overlay snapshot. Component rendering and visibility callbacks run with no
	// overlay-state lock held.
	newLines := t.composeOverlayLines(t.renderBorrowed(t.width), t.width, t.height)
	hasOverlay := t.HasOverlay()

	// Mirrors upstream TUI.extractCursorPosition (tui.ts:868): scan the
	// bottom `height` rendered lines for the APC CURSOR_MARKER, strip it
	// from the line, and remember (row, col) so we can position the
	// hardware terminal cursor at the end of the frame. Components that
	// don't emit the marker fall back to the legacy behaviour where the
	// cursor lands at the end of the last written line.
	cursorPos, cursorRow, cursorFreeLine, hasCursorPos := findCursorPosition(newLines, t.height)

	// Mirrors upstream TUI.applyLineResets (tui.ts:427): every non-image
	// line gets the SEGMENT_RESET barrier appended so SGR and OSC 8
	// hyperlink state cannot bleed between adjacent rows. Run AFTER
	// cursor extraction so the marker is gone before the suffix is added.
	// pig-specific: applyLineResetsCached reuses the previous frame's result
	// for unchanged lines (byte-identical output) so the expensive
	// normalization does not re-run on every scrolled-off history line.
	newLines = t.applyLineResetsCachedWithCursor(newLines, cursorRow, cursorFreeLine)

	width, height := t.width, t.height
	if height < 1 {
		height = 1
	}
	widthChanged := t.hasRendered && t.prevWidth != width
	heightChanged := t.hasRendered && t.prevHeight != height
	previousBufferLength := height
	if t.prevHeight > 0 {
		previousBufferLength = t.prevViewportTop + t.prevHeight
	}
	prevViewportTop := t.prevViewportTop
	if heightChanged {
		prevViewportTop = max(0, previousBufferLength-height)
	}
	hardwareCursorRow := t.hardwareCursorRow

	// First render: just write everything inline. The terminal scrolls
	// naturally as we exceed `height` lines, pushing earlier rows into
	// scrollback. Cursor lands at the last buffer row, which corresponds
	// to screen row min(len-1, height-1).
	// Mirrors upstream tui-main-screen.ts:331: the previous frame is empty
	// (first render, or everything was deleted) and no dimension changed or
	// forced redraw was requested (upstream's reset marks the width changed).
	if len(t.prevLines) == 0 && !widthChanged && !heightChanged && !t.forceRedraw {
		t.logRedraw("first render", len(newLines), height)
		t.fullRender(newLines, width, height, false)
		t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
		return
	}

	// Width changes always need a full re-render because wrapping changes.
	if widthChanged || t.forceRedraw {
		if widthChanged {
			t.logRedraw(fmt.Sprintf("terminal width changed (%d -> %d)", t.prevWidth, width), len(newLines), height)
		} else {
			t.logRedraw("forced redraw", len(newLines), height)
		}
		t.forceRedraw = false
		t.fullRender(newLines, width, height, true)
		t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
		// Notify listeners of width change (e.g. subprocess extension host).
		if widthChanged && t.onWidthChange != nil {
			go t.onWidthChange(width)
		}
		if heightChanged && t.onHeightChange != nil {
			go t.onHeightChange(height)
		}
		return
	}

	// Height changes usually need a full re-render to keep the visible
	// viewport aligned, but Termux changes height when the software keyboard
	// shows/hides. In that environment, a full redraw replays the entire
	// history on every toggle.
	if heightChanged && !isTermuxSession() {
		t.logRedraw(fmt.Sprintf("terminal height changed (%d -> %d)", t.prevHeight, height), len(newLines), height)
		t.fullRender(newLines, width, height, true)
		t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
		if t.onHeightChange != nil {
			go t.onHeightChange(height)
		}
		return
	}

	// Optionally force a full redraw when content shrinks below the working
	// area (and no overlays are active), clearing stale empty rows.
	if t.clearOnShrink && len(newLines) < t.maxLinesRendered && !hasOverlay {
		t.logRedraw(fmt.Sprintf("clearOnShrink (maxLinesRendered=%d)", t.maxLinesRendered), len(newLines), height)
		t.fullRender(newLines, width, height, true)
		t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
		return
	}

	// Find first/last changed buffer rows.
	firstChanged, lastChanged := -1, -1
	maxLen := max(len(t.prevLines), len(newLines))
	for i := range maxLen {
		var oldL, newL string
		if i < len(t.prevLines) {
			oldL = t.prevLines[i]
		}
		if i < len(newLines) {
			newL = newLines[i]
		}
		if oldL != newL {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}
	appendedLines := len(newLines) > len(t.prevLines)
	if appendedLines {
		if firstChanged == -1 {
			firstChanged = len(t.prevLines)
		}
		lastChanged = len(newLines) - 1
	}
	appendStart := appendedLines && firstChanged == len(t.prevLines) && firstChanged > 0

	if renderCaptureOn() && len(newLines) != len(t.prevLines) {
		appendRenderCapture(t.prevLines, newLines, firstChanged, lastChanged, hardwareCursorRow, prevViewportTop, height)
	}

	// Expand lastChanged to cover any previous Kitty image lines in the
	// changed range: Kitty graphics must be explicitly deleted before
	// overwriting. Mirrors upstream expandLastChangedForKittyImages.
	if firstChanged != -1 {
		firstChanged, lastChanged = t.expandChangedRangeForKittyImages(firstChanged, lastChanged, newLines)
	}

	// No content changes: still update the hardware cursor if it moved.
	if firstChanged == -1 {
		// Keep the differential frame on the buffer just published by the reset
		// cache. The cache may recycle the older buffer on the next frame.
		t.prevLines = newLines
		t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
		t.prevViewportTop = prevViewportTop
		t.prevHeight = height
		return
	}

	// All changes are deletions (firstChanged is past the new buffer
	// end). Clear the orphaned rows in place without scrolling content.
	if firstChanged >= len(newLines) {
		buf0 := t.deleteChangedKittyImages(firstChanged, lastChanged)
		if len(t.prevLines) <= len(newLines) {
			return // shouldn't happen, but bail safely
		}
		targetRow := max(len(newLines)-1, 0)
		if targetRow < prevViewportTop {
			t.fullRender(newLines, width, height, true)
			t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
			return
		}
		extraLines := len(t.prevLines) - len(newLines)
		if extraLines > height {
			t.fullRender(newLines, width, height, true)
			t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
			return
		}
		var buf strings.Builder
		buf.WriteString("\x1b[?2026h")
		buf.WriteString(buf0)
		lineDiff := computeLineDiff(targetRow, hardwareCursorRow, viewportTop(prevViewportTop), prevViewportTop)
		if lineDiff > 0 {
			fmt.Fprintf(&buf, "\x1b[%dB", lineDiff)
		} else if lineDiff < 0 {
			fmt.Fprintf(&buf, "\x1b[%dA", -lineDiff)
		}
		buf.WriteString("\r")
		// Clear orphaned rows without scrolling. A non-empty frame starts one
		// row below its retained last row; an empty frame starts at row zero.
		clearStartOffset := 1
		if len(newLines) == 0 {
			clearStartOffset = 0
		}
		if extraLines > 0 && clearStartOffset > 0 {
			fmt.Fprintf(&buf, "\x1b[%dB", clearStartOffset)
		}
		for i := range extraLines {
			buf.WriteString("\r\x1b[2K")
			if i < extraLines-1 {
				buf.WriteString("\x1b[1B")
			}
		}
		moveBack := max(0, extraLines-1+clearStartOffset)
		if moveBack > 0 {
			fmt.Fprintf(&buf, "\x1b[%dA", moveBack)
		}
		buf.WriteString("\x1b[?2026l")
		_, _ = t.out.Write([]byte(buf.String()))
		t.cursorRow = targetRow
		t.hardwareCursorRow = targetRow
		t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
		t.prevLines = newLines
		t.previousKittyImageIDs = t.collectKittyImageIDs(newLines)
		t.prevWidth = width
		t.prevHeight = height
		t.prevViewportTop = prevViewportTop
		return
	}

	// Differential rendering can only touch what was actually visible.
	// If the first changed line is above the previous viewport, match Pi's
	// clearing full redraw so obsolete physical history is removed before the
	// current logical transcript is replayed once.
	if firstChanged < prevViewportTop {
		t.logRedraw(fmt.Sprintf("firstChanged < viewportTop (%d < %d)", firstChanged, prevViewportTop), len(newLines), height)
		t.fullRender(newLines, width, height, true)
		t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
		return
	}

	// pig divergence (D53): fully clear when a formerly wrapped row now fits.
	// A logical-row clear cannot remove the old physical continuation rows.
	// D53 never replaces a differential write that Pi would end with an
	// overflow: when the upstream loop would reach an over-wide row first,
	// the differential path runs and terminates as Pi does.
	d53Allowed := t.differentialOverflowRow(newLines, firstChanged, lastChanged, appendStart, prevViewportTop, height, width) < 0
	for i := firstChanged; d53Allowed && i <= lastChanged && i < len(t.prevLines) && i < len(newLines); i++ {
		if widthx.VisibleWidth(t.prevLines[i]) > width && widthx.VisibleWidth(newLines[i]) <= width {
			t.logRedraw(fmt.Sprintf("D53 over-wide row %d replaced by a fitting row", i), len(newLines), height)
			t.fullRender(newLines, width, height, true)
			t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
			return
		}
	}

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	buf.WriteString(t.deleteChangedKittyImages(firstChanged, lastChanged))

	prevViewportBottom := prevViewportTop + height - 1
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow = firstChanged - 1
	}

	// If the target row is below the current viewport bottom, scroll
	// the buffer up by emitting `\r\n`. Each newline at the last screen
	// row scrolls older content into scrollback and advances cursor.
	if moveTargetRow > prevViewportBottom {
		currentScreenRow := min(max(hardwareCursorRow-prevViewportTop, 0), height-1)
		moveToBottom := (height - 1) - currentScreenRow
		if moveToBottom > 0 {
			fmt.Fprintf(&buf, "\x1b[%dB", moveToBottom)
		}
		scroll := moveTargetRow - prevViewportBottom
		for range scroll {
			buf.WriteString("\r\n")
		}
		prevViewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	lineDiff := computeLineDiff(moveTargetRow, hardwareCursorRow, prevViewportTop, prevViewportTop)
	if lineDiff > 0 {
		fmt.Fprintf(&buf, "\x1b[%dB", lineDiff)
	} else if lineDiff < 0 {
		fmt.Fprintf(&buf, "\x1b[%dA", -lineDiff)
	}
	if appendStart {
		buf.WriteString("\r\n")
	} else {
		buf.WriteString("\r")
	}

	renderEnd := min(lastChanged, len(newLines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		if i > firstChanged {
			buf.WriteString("\r\n")
		}
		buf.WriteString("\x1b[2K")
		line := newLines[i]
		if widthx.IsImageLine(line) {
			reservedRows := t.kittyImageReservedRows(newLines, i)
			if reservedRows > 1 {
				imageStartScreenRow := i - prevViewportTop
				if imageStartScreenRow < 0 || imageStartScreenRow+reservedRows > height {
					t.fullRender(newLines, width, height, true)
					t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
					return
				}
				buf.WriteString("\x1b[2K")
				for range reservedRows - 1 {
					buf.WriteString("\r\n\x1b[2K")
				}
				fmt.Fprintf(&buf, "\x1b[%dA", reservedRows-1)
				buf.WriteString(line)
				fmt.Fprintf(&buf, "\x1b[%dB", reservedRows-1)
				i += reservedRows - 1
				continue
			}
		}
		// Mirrors upstream tui-main-screen.ts:517-545: an over-wide non-image
		// row reaching the differential loop writes the crash log, stops the
		// TUI, and throws. The buffered frame is never written.
		if !widthx.IsImageLine(line) && widthx.VisibleWidth(line) > width {
			t.crashOnDifferentialOverflow(newLines, i, width)
		}
		buf.WriteString(line)
	}

	finalCursorRow := renderEnd

	// Shrink: clear rows beyond newLines that prevLines occupied.
	if len(t.prevLines) > len(newLines) {
		if renderEnd < len(newLines)-1 {
			moveDown := (len(newLines) - 1) - renderEnd
			fmt.Fprintf(&buf, "\x1b[%dB", moveDown)
			finalCursorRow = len(newLines) - 1
		}
		extraLines := len(t.prevLines) - len(newLines)
		for range extraLines {
			buf.WriteString("\r\n\x1b[2K")
		}
		fmt.Fprintf(&buf, "\x1b[%dA", extraLines)
	}

	buf.WriteString("\x1b[?2026l")
	_, _ = t.out.Write([]byte(buf.String()))

	t.cursorRow = max(0, len(newLines)-1)
	t.hardwareCursorRow = finalCursorRow
	if t.maxLinesRendered < len(newLines) {
		t.maxLinesRendered = len(newLines)
	}
	t.previousKittyImageIDs = t.collectKittyImageIDs(newLines)
	newViewportTop := max(max(finalCursorRow-height+1, prevViewportTop), 0)
	t.prevViewportTop = newViewportTop
	t.positionHardwareCursor(cursorPos, hasCursorPos, len(newLines))
	t.prevLines = newLines
	t.prevWidth = width
	t.prevHeight = height
}

// differentialOverflowRow returns the row at which upstream's differential
// loop (tui-main-screen.ts:473-545) would throw for this frame, or -1 when it
// completes or falls back to a full render first. It follows the loop's order:
// a multi-row Kitty image that would scroll falls back before later rows are
// checked, image rows are exempt, and only rows firstChanged..renderEnd are
// written.
func (t *TUI) differentialOverflowRow(newLines []string, firstChanged, lastChanged int, appendStart bool, prevViewportTop, height, width int) int {
	viewportTop := prevViewportTop
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow = firstChanged - 1
	}
	if prevViewportBottom := prevViewportTop + height - 1; moveTargetRow > prevViewportBottom {
		viewportTop += moveTargetRow - prevViewportBottom
	}
	renderEnd := min(lastChanged, len(newLines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		line := newLines[i]
		if widthx.IsImageLine(line) {
			if reserved := t.kittyImageReservedRows(newLines, i); reserved > 1 {
				start := i - viewportTop
				if start < 0 || start+reserved > height {
					return -1
				}
				i += reserved - 1
			}
			continue
		}
		if widthx.VisibleWidth(line) > width {
			return i
		}
	}
	return -1
}

// fullRender emits every line of newLines from a known cursor position.
// Faithful to upstream pi-tui: when clear is true, clear the visible screen,
// home the cursor, clear terminal scrollback, then replay the current logical
// render buffer once.
func (t *TUI) fullRender(newLines []string, width, height int, clear bool) {
	bufLen := max(len(newLines), height)
	viewportTop := max(0, bufLen-height)

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	if clear {
		// Delete previously tracked Kitty images before wiping the screen
		// so the terminal discards their stored data.
		buf.WriteString(t.deleteKittyImagesSet(t.previousKittyImageIDs))
		buf.WriteString("\x1b[2J\x1b[H\x1b[3J")
	}
	for i, line := range newLines {
		if i > 0 {
			buf.WriteString("\r\n")
		}
		buf.WriteString("\x1b[2K")
		// Upstream fullRender (tui-main-screen.ts:279-301) has no width check:
		// an over-wide row is emitted unchanged on initial, full, and resize renders.
		buf.WriteString(line)
	}
	buf.WriteString("\x1b[?2026l")
	_, _ = t.out.Write([]byte(buf.String()))

	t.hasRendered = true
	t.cursorRow = max(0, len(newLines)-1)
	t.hardwareCursorRow = t.cursorRow
	if clear {
		t.maxLinesRendered = len(newLines)
	} else if t.maxLinesRendered < len(newLines) {
		t.maxLinesRendered = len(newLines)
	}
	t.prevViewportTop = viewportTop
	t.prevLines = newLines
	t.previousKittyImageIDs = t.collectKittyImageIDs(newLines)
	t.prevWidth = width
	t.prevHeight = height
}

// computeLineDiff returns the number of rows the terminal cursor must
// move to land on the screen row corresponding to `targetRow`. Positive
// values mean down, negative mean up.
func computeLineDiff(targetRow, hardwareCursorRow, viewportTop, prevViewportTop int) int {
	currentScreenRow := hardwareCursorRow - prevViewportTop
	targetScreenRow := targetRow - viewportTop
	return targetScreenRow - currentScreenRow
}

func isTermuxSession() bool {
	return os.Getenv("TERMUX_VERSION") != ""
}

// positionHardwareCursor moves the real terminal cursor to `cursorPos` if
// present, else hides it. Mirrors upstream TUI.positionHardwareCursor().
//
// `cursorPos.Row` and `t.hardwareCursorRow` are both buffer-row indices, so
// the relative row delta is stable across viewport shifts.
func (t *TUI) positionHardwareCursor(cursorPos widthx.CursorPosition, ok bool, totalLines int) {
	if !ok || totalLines <= 0 {
		_, _ = fmt.Fprint(t.out, "\x1b[?25l")
		return
	}
	targetRow := max(0, min(cursorPos.Row, totalLines-1))
	targetCol := max(0, cursorPos.Col)
	rowDelta := targetRow - t.hardwareCursorRow
	var buf strings.Builder
	if rowDelta > 0 {
		fmt.Fprintf(&buf, "\x1b[%dB", rowDelta)
	} else if rowDelta < 0 {
		fmt.Fprintf(&buf, "\x1b[%dA", -rowDelta)
	}
	fmt.Fprintf(&buf, "\x1b[%dG", targetCol+1)
	if t.showHardwareCursor {
		buf.WriteString("\x1b[?25h")
	} else {
		buf.WriteString("\x1b[?25l")
	}
	if buf.Len() > 0 {
		_, _ = t.out.Write([]byte(buf.String()))
	}
	t.hardwareCursorRow = targetRow
}

// viewportTop returns prevViewportTop unchanged. Kept as a named
// helper to mirror the upstream variable name in computeLineDiff calls
// where the new viewport hasn't shifted yet.
func viewportTop(prev int) int { return prev }

// HideCursor hides the terminal cursor.
// RepaintAll forces the next Render() to redo a full paint with screen
// clear, as if it were the first frame. Used after operations that hand
// the terminal to an external editor or suspend Pig to the shell. Those
// processes may leave arbitrary
// ANSI state behind.
func (t *TUI) RepaintAll() {
	t.mu.Lock()
	t.hasRendered = false
	t.mu.Unlock()
	t.Render()
}

func (t *tuiBase) HideCursor() {
	_, _ = fmt.Fprint(t.out, "\033[?25l")
}

// ShowCursor shows the terminal cursor.
func (t *tuiBase) ShowCursor() {
	_, _ = fmt.Fprint(t.out, "\033[?25h")
}

// Stop cleanly shuts down the TUI. Moves the cursor to the end of rendered
// content, writes a newline, and shows the cursor. This preserves the screen
// content so the user sees the final state after exit.
// Mirrors upstream tui.ts stop() (lines 473-494).
// TUIRenderState is a snapshot of the main-screen renderer's inline-flow render
// state. It lets InteractiveMode preserve regular-mode scrollback position across
// a live tui-mode switch (a fullscreen round-trip discards and rebuilds the
// renderer). Mirrors upstream TuiMainScreenRenderState (tui-main-screen.ts:46).
type TUIRenderState struct {
	PrevLines         []string
	PrevWidth         int
	PrevHeight        int
	CursorRow         int
	HardwareCursorRow int
	MaxLinesRendered  int
	PrevViewportTop   int
}

// CaptureRenderState snapshots the current inline-flow render state so a renderer
// swap can restore it. Mirrors upstream TuiMainScreen.captureRenderState.
func (t *TUI) CaptureRenderState() TUIRenderState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TUIRenderState{
		PrevLines:         slices.Clone(t.prevLines),
		PrevWidth:         t.prevWidth,
		PrevHeight:        t.prevHeight,
		CursorRow:         t.cursorRow,
		HardwareCursorRow: t.hardwareCursorRow,
		MaxLinesRendered:  t.maxLinesRendered,
		PrevViewportTop:   t.prevViewportTop,
	}
}

// RestoreRenderState restores a previously captured render state so the next
// differential render is computed against the pre-switch screen. Image lines are
// blanked and the Kitty image-id set is cleared, since those images are no longer
// on the terminal after the switch. Mirrors upstream
// TuiMainScreen.restoreRenderState.
func (t *TUI) RestoreRenderState(state TUIRenderState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	restored := make([]string, len(state.PrevLines))
	for i, line := range state.PrevLines {
		if IsImageLine(line) {
			restored[i] = ""
			continue
		}
		restored[i] = line
	}
	t.prevLines = restored
	t.previousKittyImageIDs = map[int]struct{}{}
	t.prevWidth = state.PrevWidth
	t.prevHeight = state.PrevHeight
	t.cursorRow = state.CursorRow
	t.hardwareCursorRow = state.HardwareCursorRow
	t.maxLinesRendered = state.MaxLinesRendered
	t.prevViewportTop = state.PrevViewportTop
	// Non-empty restored content means the next render is a differential against
	// it, not a first-render full redraw.
	t.hasRendered = len(restored) > 0
}

func (t *TUI) Stop() { t.StopWithOptions(StopOptions{}) }

// StopWithOptions tears down the main-screen renderer. With PreserveScreen set
// (a live tui-mode switch), it skips the final cursor-park-and-newline emission
// so the swap produces no end-of-session output; mirrors upstream
// TuiMainScreen.stop({ preserveScreen }). Plain Stop() keeps the shutdown
// behavior that parks the cursor below the content.
func (t *TUI) StopWithOptions(options StopOptions) {
	t.stopped = true
	if t.renderTimer != nil {
		t.renderTimer.Stop()
		t.renderTimer = nil
	}
	// Move cursor to end of content (skipped on a preserve-screen switch).
	if !options.PreserveScreen && len(t.prevLines) > 0 {
		// Overwrite the inverted software cursor with a normal space so it
		// does not leave a highlighted-cell artifact after the hardware
		// cursor is restored on exit (mirrors upstream tui.ts stop()).
		_, _ = fmt.Fprint(t.out, " ")
		targetRow := len(t.prevLines)
		lineDiff := targetRow - t.hardwareCursorRow
		if lineDiff > 0 {
			_, _ = fmt.Fprintf(t.out, "\033[%dB", lineDiff)
		} else if lineDiff < 0 {
			_, _ = fmt.Fprintf(t.out, "\033[%dA", -lineDiff)
		}
		_, _ = fmt.Fprint(t.out, "\r\n")
	}
	t.ShowCursor()
}

// drawBox draws a box with title and content lines. Content clipping and padding use terminal-cell width and preserve ANSI styling without splitting graphemes.
func drawBox(title string, lines []string, width, maxHeight int) []string {
	inner := width - 2
	// Top border
	top := "┌"
	if title != "" {
		t := " " + title + " "
		if widthx.VisibleWidth(t) > inner-2 {
			t = widthx.TruncateToWidth(t, max(inner-2, 0), "", false)
		}
		top += t + strings.Repeat("─", max(inner-widthx.VisibleWidth(t), 0)) + "┐"
	} else {
		top += strings.Repeat("─", inner) + "┐"
	}

	out := []string{top}
	for _, l := range lines {
		if len(out) >= maxHeight-1 {
			break
		}
		visible := widthx.VisibleWidth(l)
		if visible > inner {
			l = widthx.TruncateToWidth(l, inner, "", false)
			visible = widthx.VisibleWidth(l)
		}
		if visible < inner {
			l += strings.Repeat(" ", inner-visible)
		}
		out = append(out, "│"+l+"│")
	}
	// Pad remaining rows
	for len(out) < maxHeight-1 {
		out = append(out, "│"+strings.Repeat(" ", inner)+"│")
	}
	out = append(out, "└"+strings.Repeat("─", inner)+"┘")
	return out
}

// overlayLine composites an overlay payload over a background row.
//
// The previous implementation
// stripped ANSI from `base` then per-rune-overwrote a center band, which
// (a) left the bg row's plain text visible left+right of the modal box
// and (b) sliced the overlay's own ANSI escapes mid-sequence when an
// escape rune happened to land in a single cell. Result: chat content
// peeked through the modal and overlay colors were mangled.
//
// Modal overlays should fully blank the row width: only rows OUTSIDE
// the overlay's vertical range pass through unchanged (handled by the
// caller in renderOverlay). For affected rows we emit:
//
//	\x1b[0m  + spaces(col)  + overlay  + \x1b[0m  + spaces(remainder)
//
// The leading reset discards any inherited SGR state from the previous
// line; the trailing reset stops the overlay's last SGR from bleeding
// into the right margin. Width is calculated from the ANSI-stripped
// overlay so multi-byte / styled content composites correctly.
//
// Mirrors upstream `packages/tui/src/tui.ts::compositeLineAt` minus the
// before/after segment preservation: we don't need bg styling outside
// the modal because every covered row is fully replaced.
func overlayLine(base, overlay string, col, termW int) string {
	_ = base // intentionally discarded: see doc-comment
	overlayWidth := widthx.VisibleWidth(overlay)
	if col < 0 {
		col = 0
	}
	var b strings.Builder
	b.Grow(len(overlay) + termW + 8)
	b.WriteString("\x1b[0m")
	if col > 0 {
		b.WriteString(strings.Repeat(" ", col))
	}
	b.WriteString(overlay)
	b.WriteString("\x1b[0m")
	rem := termW - col - overlayWidth
	if rem > 0 {
		b.WriteString(strings.Repeat(" ", rem))
	}
	return b.String()
}

// stripANSI removes recognized ANSI/OSC/APC sequences from s.

// extractKittyImageIDs parses a single rendered line and returns any Kitty
// image IDs embedded in it. Mirrors upstream extractKittyImageIds.
type kittyImageHeader struct {
	ids  []int
	rows int
}

func parseKittyImageHeader(line string) kittyImageHeader {
	const prefix = "\x1b_G"
	idx := strings.Index(line, prefix)
	if idx == -1 {
		return kittyImageHeader{rows: 1}
	}
	paramsStart := idx + len(prefix)
	paramsEnd := strings.Index(line[paramsStart:], ";")
	if paramsEnd == -1 {
		return kittyImageHeader{rows: 1}
	}
	params := line[paramsStart : paramsStart+paramsEnd]
	header := kittyImageHeader{rows: 1}
	for param := range strings.SplitSeq(params, ",") {
		key, val, ok := strings.Cut(param, "=")
		if !ok || val == "" {
			continue
		}
		id := 0
		for _, c := range val {
			if c < '0' || c > '9' {
				id = 0
				break
			}
			id = id*10 + int(c-'0')
		}
		if id <= 0 || id > 0xffffffff {
			continue
		}
		switch key {
		case "i":
			header.ids = append(header.ids, id)
		case "r":
			header.rows = id
		}
	}
	return header
}

func extractKittyImageIDs(line string) []int {
	return parseKittyImageHeader(line).ids
}

// kittyImagesActive reports whether the terminal uses the Kitty graphics
// protocol. The Kitty image escape (\x1b_G) is emitted only when
// GetCapabilities().Images == ImageProtocolKitty (see Image.Render). On every
// other terminal no such sequence can appear in the rendered buffer, so the
// per-frame full-buffer kitty scans (collect/expand/delete) are dead work.
// Gating on this cached capability removes an O(total lines) string scan that
// dominated the render frame on long sessions (~34% of CPU in profiling).
func kittyImagesActive() bool { return GetCapabilities().Images == ImageProtocolKitty }

// collectKittyImageIDs scans lines and returns the set of all Kitty image IDs
// present. Mirrors upstream TUI.collectKittyImageIds.
func (t *TUI) collectKittyImageIDs(lines []string) map[int]struct{} {
	ids := make(map[int]struct{})
	if !kittyImagesActive() {
		return ids
	}
	for _, line := range lines {
		for _, id := range extractKittyImageIDs(line) {
			ids[id] = struct{}{}
		}
	}
	return ids
}

// deleteKittyImagesSet emits delete sequences for all IDs in the set.
func (t *TUI) deleteKittyImagesSet(ids map[int]struct{}) string {
	var b strings.Builder
	for id := range ids {
		b.WriteString(DeleteKittyImage(id))
	}
	return b.String()
}

// expandLastChangedForKittyImages expands lastChanged to include any
// previous lines containing Kitty images in [firstChanged, end).
// Mirrors upstream TUI.expandLastChangedForKittyImages.
func (t *TUI) kittyImageReservedRows(lines []string, index int) int {
	rows := parseKittyImageHeader(lines[index]).rows
	if rows <= 1 {
		return 1
	}
	maxRows := min(rows, len(lines)-index)
	reserved := 1
	for reserved < maxRows {
		line := lines[index+reserved]
		if widthx.IsImageLine(line) || widthx.VisibleWidth(line) > 0 {
			break
		}
		reserved++
	}
	return reserved
}

func (t *TUI) expandChangedRangeForKittyImages(firstChanged, lastChanged int, newLines []string) (int, int) {
	if !kittyImagesActive() {
		return firstChanged, lastChanged
	}
	expandedFirst, expandedLast := firstChanged, lastChanged
	for _, lines := range [][]string{t.prevLines, newLines} {
		for i := range lines {
			if len(extractKittyImageIDs(lines[i])) == 0 {
				continue
			}
			blockEnd := i + t.kittyImageReservedRows(lines, i) - 1
			if i >= firstChanged || i <= lastChanged && blockEnd >= firstChanged {
				expandedFirst = min(expandedFirst, i)
				expandedLast = max(expandedLast, blockEnd)
			}
		}
	}
	return expandedFirst, expandedLast
}

// deleteChangedKittyImages returns delete sequences for all Kitty images
// that appear in prevLines[firstChanged..lastChanged].
// Mirrors upstream TUI.deleteChangedKittyImages.
func (t *TUI) deleteChangedKittyImages(firstChanged, lastChanged int) string {
	if !kittyImagesActive() {
		return ""
	}
	if firstChanged < 0 || lastChanged < firstChanged {
		return ""
	}
	ids := make(map[int]struct{})
	maxLine := min(lastChanged, len(t.prevLines)-1)
	for i := firstChanged; i <= maxLine; i++ {
		for _, id := range extractKittyImageIDs(t.prevLines[i]) {
			ids[id] = struct{}{}
		}
	}
	var b strings.Builder
	for id := range ids {
		b.WriteString(DeleteKittyImage(id))
	}
	return b.String()
}

// Delegates to widthx.StripAnsi. The legacy implementation handled only
// CSI m/K/H/J finals: it silently miscounted strings containing OSC 8
// hyperlinks or APC cursor markers. Callers that want a visible-column
// width should use widthx.VisibleWidth(s) directly instead of the
// `runewidth.StringWidth(stripANSI(s))` pattern.
func stripANSI(s string) string {
	return widthx.StripAnsi(s)
}

// detectKitty checks if the terminal supports the Kitty graphics protocol.
func detectKitty() bool {
	term := os.Getenv("TERM")
	termProg := os.Getenv("TERM_PROGRAM")
	return term == "xterm-kitty" || termProg == "ghostty" || termProg == "kitty"
}
