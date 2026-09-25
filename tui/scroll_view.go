package tui

import (
	"fmt"
	"sync"
	"time"
)

// Ports pi-tui's scroll-view.ts: a single-child vertical scroll viewport with an
// optional auto-hiding transient scrollbar. Names and structure mirror upstream
// 1:1. Forced Go mechanics: the scrollbar-hide setTimeout becomes an owned
// time.AfterFunc guarded by a mutex plus a generation counter, because
// time.AfterFunc fires on a separate goroutine and Stop() races a fired-but-
// pending callback (JS setTimeout runs on the single event loop, so upstream
// needs neither); the four upstream throws become panics with identical
// messages; and the "hidden"|"auto"|"always" scrollbar mode stays a string.

// ScrollViewOptions configure a ScrollView. Empty string fields take upstream's
// default; ScrollbarHideDelayMs nil defaults to 1000ms.
type ScrollViewOptions struct {
	Axis                 string // "" (unset) or "vertical"
	Follow               string // "none" | "end"
	Primary              bool
	Overscroll           string // "chain" | "contain"
	Scrollbar            string // "hidden" | "auto" | "always"
	ScrollbarTrackStyle  func(text string) string
	ScrollbarThumbStyle  func(text string) string
	ScrollbarHideDelayMs *int
}

// ScrollViewScrollToOptions mirrors upstream ScrollViewScrollToOptions.
type ScrollViewScrollToOptions struct {
	// DisableFollow keeps follow-end disabled even when the target is the
	// current content end.
	DisableFollow bool
}

// ScrollView ports pi-tui's ScrollView. It embeds pig's Container (holding the
// single child) and owns the transient-scrollbar timer.
type ScrollView struct {
	*Container
	child                Component
	followEnd            bool
	primary              bool
	overscroll           string
	scrollbarTrackStyle  func(text string) string
	scrollbarThumbStyle  func(text string) string
	scrollbarHideDelayMs int

	mu                        sync.Mutex
	currentScrollbar          string
	currentScrollTop          int
	contentHeight             int
	currentViewportHeight     int
	followingEnd              bool
	followSuppressedAtEnd     bool
	requestRenderCallback     func()
	transientScrollbarVisible bool
	scrollbarActive           bool
	scrollbarHideTimer        *time.Timer
	scrollbarHideGen          int // invalidates a fired-but-pending hide callback
}

// NewScrollView constructs a ScrollView. It panics on an unsupported axis,
// mirroring upstream's constructor throw.
func NewScrollView(component Component, options ScrollViewOptions) *ScrollView {
	if options.Axis != "" && options.Axis != "vertical" {
		panic(fmt.Sprintf("Unsupported ScrollView axis: %s", options.Axis))
	}
	follow := options.Follow
	if follow == "" {
		follow = "none"
	}
	overscroll := options.Overscroll
	if overscroll == "" {
		overscroll = "chain"
	}
	scrollbar := options.Scrollbar
	if scrollbar == "" {
		scrollbar = "hidden"
	}
	trackStyle := options.ScrollbarTrackStyle
	if trackStyle == nil {
		trackStyle = func(text string) string { return "\x1b[90m" + text + "\x1b[39m" }
	}
	thumbStyle := options.ScrollbarThumbStyle
	if thumbStyle == nil {
		thumbStyle = func(text string) string { return "\x1b[37m" + text + "\x1b[39m" }
	}
	delay := 1000
	if options.ScrollbarHideDelayMs != nil {
		delay = *options.ScrollbarHideDelayMs
	}
	sv := &ScrollView{
		Container:            NewContainer(component),
		child:                component,
		followEnd:            follow == "end",
		primary:              options.Primary,
		overscroll:           overscroll,
		scrollbarTrackStyle:  trackStyle,
		scrollbarThumbStyle:  thumbStyle,
		scrollbarHideDelayMs: max(0, delay),
		currentScrollbar:     scrollbar,
	}
	sv.followingEnd = sv.followEnd
	return sv
}

// ScrollTop reports the current scroll offset.
func (s *ScrollView) ScrollTop() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentScrollTop
}

// Primary reports whether this is the primary scroll view.
func (s *ScrollView) Primary() bool { return s.primary }

// Overscroll reports the overscroll mode.
func (s *ScrollView) Overscroll() string { return s.overscroll }

// ViewportHeight reports the current viewport height.
func (s *ScrollView) ViewportHeight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentViewportHeight
}

// IsFollowingEnd reports whether the view is pinned to the end.
func (s *ScrollView) IsFollowingEnd() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.followingEnd
}

// Scrollbar reports the current scrollbar mode.
func (s *ScrollView) Scrollbar() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentScrollbar
}

// FollowEnd reports whether the view was configured to follow its end.
func (s *ScrollView) FollowEnd() bool { return s.followEnd }

// ScrollbarTrackStyle returns the style function applied to scrollbar track cells.
func (s *ScrollView) ScrollbarTrackStyle() func(text string) string { return s.scrollbarTrackStyle }

// ScrollbarThumbStyle returns the style function applied to scrollbar thumb cells.
func (s *ScrollView) ScrollbarThumbStyle() func(text string) string { return s.scrollbarThumbStyle }

// IsScrollbarActive reports whether the pointer is hovering or dragging the scrollbar.
func (s *ScrollView) IsScrollbarActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scrollbarActive
}

// IsScrollbarVisible reports whether the scrollbar should paint this frame.
func (s *ScrollView) IsScrollbarVisible() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentScrollbar == "always" {
		return s.currentViewportHeight > 0
	}
	return s.currentScrollbar == "auto" && s.contentHeight > s.currentViewportHeight && s.transientScrollbarVisible
}

// SetScrollbar changes the scrollbar mode.
func (s *ScrollView) SetScrollbar(scrollbar string) {
	s.mu.Lock()
	if scrollbar == s.currentScrollbar {
		s.mu.Unlock()
		return
	}
	s.currentScrollbar = scrollbar
	if scrollbar != "auto" {
		s.hideTransientScrollbar()
	} else if s.scrollbarActive {
		s.markScrollbarActivity()
	}
	cb := s.requestRenderCallback
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// GetContentWidth returns the child render width, reserving a column for an
// always-on scrollbar.
func (s *ScrollView) GetContentWidth(width int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentScrollbar == "always" && width > 1 {
		return width - 1
	}
	return width
}

// markScrollbarActivity shows the transient scrollbar and arms the hide timer.
// The caller must hold s.mu.
func (s *ScrollView) markScrollbarActivity() {
	if s.currentScrollbar != "auto" || s.contentHeight <= s.currentViewportHeight {
		return
	}
	s.transientScrollbarVisible = true
	s.stopHideTimer()
	if s.scrollbarActive {
		return
	}
	gen := s.scrollbarHideGen
	s.scrollbarHideTimer = time.AfterFunc(time.Duration(s.scrollbarHideDelayMs)*time.Millisecond, func() {
		s.onHideTimer(gen)
	})
}

// stopHideTimer stops any armed hide timer and invalidates a pending callback.
// The caller must hold s.mu.
func (s *ScrollView) stopHideTimer() {
	if s.scrollbarHideTimer != nil {
		s.scrollbarHideTimer.Stop()
		s.scrollbarHideTimer = nil
	}
	s.scrollbarHideGen++
}

// onHideTimer fires off the timer goroutine; it hides the transient scrollbar
// unless a newer activity has superseded this generation.
func (s *ScrollView) onHideTimer(gen int) {
	s.mu.Lock()
	if gen != s.scrollbarHideGen {
		s.mu.Unlock()
		return
	}
	s.scrollbarHideTimer = nil
	s.transientScrollbarVisible = false
	cb := s.requestRenderCallback
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// hideTransientScrollbar hides the scrollbar and cancels the hide timer.
// The caller must hold s.mu.
func (s *ScrollView) hideTransientScrollbar() {
	s.transientScrollbarVisible = false
	s.stopHideTimer()
}

// SetScrollbarActive marks the scrollbar as actively dragged (keeping it shown).
func (s *ScrollView) SetScrollbarActive(active bool) {
	s.mu.Lock()
	if active == s.scrollbarActive {
		s.mu.Unlock()
		return
	}
	s.scrollbarActive = active
	s.markScrollbarActivity()
	cb := s.requestRenderCallback
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// ScrollTo scrolls to an absolute offset, clamped to the scrollable range.
func (s *ScrollView) ScrollTo(scrollTop int) {
	s.ScrollToWithOptions(scrollTop, ScrollViewScrollToOptions{})
}

// ScrollToWithOptions scrolls to an absolute offset, clamped to the scrollable
// range. Mirrors upstream scrollTo(scrollTop, options).
func (s *ScrollView) ScrollToWithOptions(scrollTop int, options ScrollViewScrollToOptions) {
	s.mu.Lock()
	maxScrollTop := max(0, s.contentHeight-s.currentViewportHeight)
	next := max(0, min(maxScrollTop, scrollTop))
	nextFollowSuppressedAtEnd := options.DisableFollow && next == maxScrollTop
	nextFollowingEnd := !nextFollowSuppressedAtEnd && s.followEnd && next == maxScrollTop
	if next == s.currentScrollTop && nextFollowingEnd == s.followingEnd &&
		nextFollowSuppressedAtEnd == s.followSuppressedAtEnd {
		s.mu.Unlock()
		return
	}
	moved := next != s.currentScrollTop
	s.currentScrollTop = next
	s.followingEnd = nextFollowingEnd
	s.followSuppressedAtEnd = nextFollowSuppressedAtEnd
	if moved {
		s.markScrollbarActivity()
	}
	cb := s.requestRenderCallback
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// ScrollBy scrolls by a relative number of lines and returns the leftover lines
// that could not be applied (for overscroll chaining).
func (s *ScrollView) ScrollBy(lines int) int {
	s.mu.Lock()
	if lines == 0 {
		s.mu.Unlock()
		return 0
	}
	maxScrollTop := max(0, s.contentHeight-s.currentViewportHeight)
	start := s.currentScrollTop
	if s.followingEnd {
		start = maxScrollTop
	}
	next := max(0, min(maxScrollTop, start+lines))
	moved := next - start
	wasFollowingEnd := s.followingEnd
	s.currentScrollTop = next
	s.followingEnd = s.followEnd && next == maxScrollTop
	s.followSuppressedAtEnd = false
	if moved != 0 {
		s.markScrollbarActivity()
	}
	var cb func()
	if moved != 0 || s.followingEnd != wasFollowingEnd {
		cb = s.requestRenderCallback
	}
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
	return lines - moved
}

// ScrollToStart scrolls to the top.
func (s *ScrollView) ScrollToStart() {
	s.mu.Lock()
	changed := s.currentScrollTop != 0 ||
		s.followingEnd != (s.followEnd && s.contentHeight <= s.currentViewportHeight)
	s.currentScrollTop = 0
	s.followingEnd = s.followEnd && s.contentHeight <= s.currentViewportHeight
	s.followSuppressedAtEnd = false
	var cb func()
	if changed {
		s.markScrollbarActivity()
		cb = s.requestRenderCallback
	}
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// ScrollToEnd scrolls to the bottom.
func (s *ScrollView) ScrollToEnd() {
	s.mu.Lock()
	next := max(0, s.contentHeight-s.currentViewportHeight)
	changed := s.currentScrollTop != next || s.followingEnd != s.followEnd
	s.currentScrollTop = next
	s.followingEnd = s.followEnd
	s.followSuppressedAtEnd = false
	var cb func()
	if changed {
		s.markScrollbarActivity()
		cb = s.requestRenderCallback
	}
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// UpdateLayout is called by the layout engine each pass with the measured
// content and viewport heights; it clamps the scroll offset and stores the
// render callback the hide timer will invoke.
func (s *ScrollView) UpdateLayout(contentHeight, viewportHeight int, requestRender func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contentHeight = max(0, contentHeight)
	s.currentViewportHeight = max(0, viewportHeight)
	s.requestRenderCallback = requestRender
	maxScrollTop := max(0, s.contentHeight-s.currentViewportHeight)
	if s.followingEnd {
		s.currentScrollTop = maxScrollTop
	} else {
		s.currentScrollTop = max(0, min(s.currentScrollTop, maxScrollTop))
	}
	if s.currentScrollTop < maxScrollTop {
		s.followSuppressedAtEnd = false
	}
	if s.followEnd && s.currentScrollTop == maxScrollTop && !s.followSuppressedAtEnd {
		s.followingEnd = true
	}
	if s.contentHeight <= s.currentViewportHeight {
		s.hideTransientScrollbar()
	}
}

// Add panics: a ScrollView has exactly one child (upstream throw).
func (s *ScrollView) Add(Component) { panic("ScrollView has exactly one child") }

// Remove panics: a ScrollView's child cannot be removed (upstream throw).
func (s *ScrollView) Remove(Component) { panic("ScrollView child cannot be removed") }

// Clear panics: a ScrollView's child cannot be cleared (upstream throw).
func (s *ScrollView) Clear() { panic("ScrollView child cannot be cleared") }

// Render ports upstream ScrollView.render: render the child at the content width,
// padding each line by a column when a scrollbar column is reserved.
func (s *ScrollView) Render(width int) []string {
	contentWidth := s.GetContentWidth(width)
	lines := s.child.Render(contentWidth)
	if contentWidth == width {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line + " "
	}
	return out
}

// LayoutNode ports upstream ScrollView[LAYOUT_NODE](): a scroll node whose state
// is this ScrollView narrowed to ScrollLayoutState.
func (s *ScrollView) LayoutNode() LayoutNode {
	return ScrollLayoutNode{Component: s.child, State: s}
}

// Dispose stops the hide timer so no goroutine outlives the view.
func (s *ScrollView) Dispose() {
	s.mu.Lock()
	s.stopHideTimer()
	s.mu.Unlock()
}

// Compile-time proof ScrollView satisfies the layout contracts.
var (
	_ ScrollLayoutState = (*ScrollView)(nil)
	_ LayoutComponent   = (*ScrollView)(nil)
)
