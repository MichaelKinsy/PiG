package tui

import (
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Mouse routing for TuiAltScreen: component dispatch, the scroll-to-end
// indicator, scrollbar hover and dragging, wheel scrolling, and right-click
// paste. Mirrors the mouse half of upstream tui-alt-screen.ts.
//
// Locking: t.mu guards state that doRender or the auto-scroll tick reads.
// Component handlers, ScrollView mutations, OverlayHandle calls, and
// RequestRender run with t.mu released, because each can call back into
// RequestRender. The component gesture fields (mouseCapture, mousePressTarget,
// mousePressPoint, mousePressMoved, lastComponentClick) are touched only from
// HandleViewportInput on the owner loop and need no lock.

const (
	altWheelScrollMultiplier = 5
	altDoubleClickInterval   = 500 * time.Millisecond
)

// altSgrMousePattern matches an SGR mouse report: \x1b[<button;x;yM|m.
var altSgrMousePattern = regexp.MustCompile(`^\x1b\[<(\d+);(\d+);(\d+)([Mm])$`)

// pointerXY is a screen pointer position.
type pointerXY struct{ x, y int }

// scrollbarDrag tracks an in-progress scrollbar thumb drag. grabOffset is the
// cell distance from the thumb top to the grab point. Mirrors upstream
// ScrollbarDrag.
type scrollbarDrag struct {
	scrollView *ScrollView
	grabOffset int
}

// scrollbarActivity is one deferred ScrollView.SetScrollbarActive call.
type scrollbarActivity struct {
	scrollView *ScrollView
	active     bool
}

// scrollToEndIndicatorRect is where the jump-to-end label was painted.
type scrollToEndIndicatorRect struct {
	row, column, width int
}

// componentClick is the last click delivered to a component-owned control.
type componentClick struct {
	timestamp time.Time
	count     int
	component Component
	x, y      int
}

// sgrMouseEvent is a decoded SGR mouse report. x/y are zero-based cells;
// release is true for the final-byte 'm' report. Mirrors upstream
// SgrMouseEvent.
type sgrMouseEvent struct {
	button  int
	x       int
	y       int
	release bool
}

// wheelEvent is a decoded vertical wheel report. Mirrors upstream WheelEvent.
type wheelEvent struct {
	direction int
	x, y      int
	button    int
}

// mouseEffects accumulates side effects a selection or scrollbar handler
// decides on while holding t.mu; the caller applies them after unlocking.
type mouseEffects struct {
	render        bool
	selectionText string
	openURL       string
	openURLSet    bool
	scrollSV      *ScrollView
	scrollTop     int
	doScrollTo    bool
	clickEvent    *TuiMouseEvent
}

// unlockAndApplyHover releases t.mu and then applies queued scrollbar hover
// transitions in order. ScrollView.SetScrollbarActive requests a render, which
// locks t.mu, so transitions decided under the lock apply here.
func (t *TuiAltScreen) unlockAndApplyHover() {
	pending := t.pendingScrollbarActivity
	t.pendingScrollbarActivity = nil
	t.mu.Unlock()
	for _, activity := range pending {
		activity.scrollView.SetScrollbarActive(activity.active)
	}
}

// parseSgrMouseEvent decodes an SGR mouse report. Mirrors upstream
// parseSgrMouseEvent.
func parseSgrMouseEvent(data string) (sgrMouseEvent, bool) {
	m := altSgrMousePattern.FindStringSubmatch(data)
	if m == nil {
		return sgrMouseEvent{}, false
	}
	return sgrMouseEvent{button: atoiDigits(m[1]), x: atoiDigits(m[2]) - 1, y: atoiDigits(m[3]) - 1, release: m[4] == "m"}, true
}

// parseWheelEvent decodes an SGR or legacy vertical wheel report; horizontal
// wheel buttons are not wheel events. Mirrors upstream parseWheelEvent.
func parseWheelEvent(data string) (wheelEvent, bool) {
	var button, x, y int
	if m := altSgrMousePattern.FindStringSubmatch(data); m != nil {
		button, x, y = atoiDigits(m[1]), atoiDigits(m[2])-1, atoiDigits(m[3])-1
	} else if len(data) == 6 && strings.HasPrefix(data, "\x1b[M") {
		button, x, y = int(data[3])-32, int(data[4])-33, int(data[5])-33
	} else {
		return wheelEvent{}, false
	}
	if button&64 == 0 || button&3 > 1 {
		return wheelEvent{}, false
	}
	direction := 1
	if button&3 == 0 {
		direction = -1
	}
	return wheelEvent{direction: direction, x: x, y: y, button: button}, true
}

// isMouseSequence reports whether data is any mouse report, so unhandled
// reports are consumed rather than typed. Mirrors upstream isMouseSequence.
func isMouseSequence(data string) bool {
	return altSgrMousePattern.MatchString(data) || (len(data) == 6 && strings.HasPrefix(data, "\x1b[M"))
}

func atoiDigits(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// altMouseEnableSequence picks button-motion tracking inside terminal
// multiplexers, which lag when every pointer movement is forwarded, and
// all-motion tracking elsewhere. Mirrors upstream beforeTerminalStart.
func altMouseEnableSequence() string {
	term := strings.ToLower(os.Getenv("TERM"))
	_, tmux := os.LookupEnv("TMUX")
	_, zellij := os.LookupEnv("ZELLIJ")
	_, screen := os.LookupEnv("STY")
	if tmux || zellij || screen || strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "screen") {
		return altEnableButtonMotionMouse
	}
	return altEnableAllMotionMouse
}

func decodeMouseButton(button int) TuiMouseButton {
	switch button & 3 {
	case 0:
		return MouseButtonLeft
	case 1:
		return MouseButtonMiddle
	case 2:
		return MouseButtonRight
	default:
		return MouseButtonNone
	}
}

// createMouseEvent builds a screen-level event. Mirrors upstream
// createMouseEvent.
func (t *TuiAltScreen) createMouseEvent(eventType TuiMouseEventType, button, x, y, wheelDelta, clickCount int) TuiMouseEvent {
	t.mu.Lock()
	width, height := max(1, t.width), max(1, t.height)
	t.mu.Unlock()
	decoded := decodeMouseButton(button)
	if eventType == MouseWheel {
		decoded = MouseButtonNone
	}
	return TuiMouseEvent{
		Type: eventType, Button: decoded, X: x, Y: y, ScreenX: x, ScreenY: y, Width: width, Height: height,
		Shift: button&4 != 0, Alt: button&8 != 0, Ctrl: button&16 != 0,
		WheelDelta: wheelDelta, ClickCount: clickCount,
	}
}

func (t *TuiAltScreen) layoutSnapshot() *LayoutFrame {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.currentLayout
}

// usesContainerMouseHandler reports layout nodes whose HandleMouse is the
// promoted Container handler; layout dispatch visits their boxes directly.
func usesContainerMouseHandler(component Component) bool {
	switch component.(type) {
	case *Stack, *HStack, *VStack, *ScrollView:
		return true
	}
	return false
}

// dispatchMouseToLayout dispatches along the visual hit path, deepest box
// first. Mirrors upstream dispatchMouseToLayout.
func (t *TuiAltScreen) dispatchMouseToLayout(event TuiMouseEvent) *TuiMouseDispatchResult {
	layout := t.layoutSnapshot()
	if layout == nil {
		return nil
	}
	visited := map[Component]bool{}
	for _, box := range GetLayoutBoxesAt(*layout, event.ScreenX, event.ScreenY) {
		if visited[box.Component] {
			continue
		}
		if getLayoutNode(box.Component) != nil && usesContainerMouseHandler(box.Component) {
			continue
		}
		visited[box.Component] = true
		local := event
		local.X = event.ScreenX - box.Rect.X
		local.Y = event.ScreenY - box.Rect.Y
		local.Width = box.Rect.Width
		local.Height = box.Rect.Height
		if result := DispatchMouseEvent(box.Component, local); result != nil {
			return result
		}
	}
	return nil
}

// dispatchMouseToScreen tries the overlays, then the layout when no overlay
// was hit.
func (t *TuiAltScreen) dispatchMouseToScreen(event TuiMouseEvent) *TuiMouseDispatchResult {
	overlay := t.dispatchMouseToOverlay(event)
	if overlay.result != nil || overlay.hit {
		return overlay.result
	}
	return t.dispatchMouseToLayout(event)
}

// applyMouseDispatchResult applies focus and capture and reports whether to
// render. Mirrors upstream applyMouseDispatchResult.
func (t *TuiAltScreen) applyMouseDispatchResult(event TuiMouseEvent, result *TuiMouseDispatchResult) bool {
	focusTarget := result.FocusTarget
	if focusTarget == nil {
		focusTarget = result.Target.Component
	}
	focusTarget = t.resolveMouseFocusTarget(focusTarget)
	focusChanged := result.Focus && t.FocusedComponent() != focusTarget
	if result.Focus {
		t.SetFocus(focusTarget)
	}
	if result.Capture {
		target := result.Target
		t.mouseCapture = &target
	}
	if result.Render != nil {
		return *result.Render
	}
	switch event.Type {
	case MousePress, MouseClick, MouseDrag, MouseWheel:
		return true
	}
	return focusChanged
}

// getComponentClickCount counts consecutive clicks on the same control cell
// within the double-click interval, cycling 1-2-3. Mirrors upstream
// getComponentClickCount.
func (t *TuiAltScreen) getComponentClickCount(target TuiMouseDispatchTarget, x, y int) int {
	now := t.now()
	previous := t.lastComponentClick
	count := 1
	if previous != nil && now.Sub(previous.timestamp) <= altDoubleClickInterval &&
		previous.component == target.Component && previous.x == x && previous.y == y {
		count = previous.count%3 + 1
	}
	t.lastComponentClick = &componentClick{timestamp: now, count: count, component: target.Component, x: x, y: y}
	return count
}

func (t *TuiAltScreen) clearComponentMouseGesture() {
	t.mouseCapture = nil
	t.mousePressTarget = nil
	t.mousePressPoint = nil
	t.mousePressMoved = false
}

// handleCapturedMouseEvent routes an event to the component that captured the
// gesture or received its press, synthesizing a click on an unmoved release.
func (t *TuiAltScreen) handleCapturedMouseEvent(raw sgrMouseEvent, event TuiMouseEvent) {
	target := t.mousePressTarget
	if t.mouseCapture != nil {
		target = t.mouseCapture
	}
	captured := *target
	if t.mousePressPoint != nil && (raw.x != t.mousePressPoint.x || raw.y != t.mousePressPoint.y) {
		t.mousePressMoved = true
		t.lastComponentClick = nil
	}
	render := false
	if result := DispatchMouseEvent(captured.Component, RetargetMouseEvent(event, captured)); result != nil {
		render = t.applyMouseDispatchResult(event, result)
	}
	if raw.release {
		if !t.mousePressMoved && t.mousePressPoint != nil && t.mousePressPoint.x == raw.x && t.mousePressPoint.y == raw.y {
			click := t.createMouseEvent(MouseClick, raw.button, raw.x, raw.y, 0, t.getComponentClickCount(captured, raw.x, raw.y))
			if result := DispatchMouseEvent(captured.Component, RetargetMouseEvent(click, captured)); result != nil {
				render = t.applyMouseDispatchResult(click, result) || render
			}
		}
		t.clearComponentMouseGesture()
	}
	if render {
		t.RequestRender()
	}
}

// handleMouseEvent routes a non-wheel mouse report. Mirrors upstream
// handleMouseEvent.
func (t *TuiAltScreen) handleMouseEvent(raw sgrMouseEvent) {
	eventType := MousePress
	switch {
	case raw.release:
		eventType = MouseRelease
	case raw.button&32 != 0 && decodeMouseButton(raw.button) == MouseButtonNone:
		eventType = MouseMove
	case raw.button&32 != 0:
		eventType = MouseDrag
	}
	event := t.createMouseEvent(eventType, raw.button, raw.x, raw.y, 0, 0)
	if t.mouseCapture != nil || t.mousePressTarget != nil {
		t.handleCapturedMouseEvent(raw, event)
		return
	}
	if t.handleSearchMouseEvent(raw) {
		return
	}
	overlay := t.dispatchMouseToOverlay(event)
	if !overlay.hit {
		if t.handleScrollToEndIndicatorMouseEvent(raw) || t.handleScrollbarPointer(raw) {
			return
		}
	} else {
		t.mu.Lock()
		t.stopScrollbarHover()
		t.unlockAndApplyHover()
	}
	result := overlay.result
	if result == nil && !overlay.hit {
		result = t.dispatchMouseToLayout(event)
	}
	if result != nil {
		render := t.applyMouseDispatchResult(event, result)
		if eventType == MousePress {
			t.mu.Lock()
			t.clearTextSelectionLocked()
			t.mu.Unlock()
			target := result.Target
			t.mousePressTarget = &target
			t.mousePressPoint = &pointerXY{x: raw.x, y: raw.y}
			t.mousePressMoved = false
		}
		if render {
			t.RequestRender()
		}
		return
	}
	if t.handleRightClickPaste(raw) {
		return
	}
	t.handleSelectionPointer(raw)
}

// handleScrollbarPointer runs scrollbar drag/press handling and hover, and
// reports whether the scrollbar consumed the event.
func (t *TuiAltScreen) handleScrollbarPointer(raw sgrMouseEvent) bool {
	t.mu.Lock()
	var eff mouseEffects
	handled := t.handleScrollbarMouseEvent(raw, &eff)
	if t.scrollbarDrag == nil {
		t.updateScrollbarHover(raw.x, raw.y)
	}
	t.unlockAndApplyHover()
	if eff.doScrollTo && eff.scrollSV != nil {
		eff.scrollSV.ScrollTo(eff.scrollTop)
	}
	return handled
}

// handleSelectionPointer runs the selection state machine and applies its
// effects: link activation, click delivery to a control under a zero-width
// selection, and copy-on-select.
func (t *TuiAltScreen) handleSelectionPointer(raw sgrMouseEvent) {
	t.mu.Lock()
	var eff mouseEffects
	t.handleSelectionMouseEvent(raw, &eff)
	t.unlockAndApplyHover()
	if eff.openURLSet && t.openURL != nil {
		t.safeOpenURL(eff.openURL)
	}
	if eff.clickEvent != nil {
		if result := t.dispatchMouseToScreen(*eff.clickEvent); result != nil {
			render := t.applyMouseDispatchResult(*eff.clickEvent, result)
			t.mu.Lock()
			t.clearTextSelectionLocked()
			t.mu.Unlock()
			if render {
				t.RequestRender()
			}
			return
		}
	}
	if eff.selectionText != "" {
		t.copyTextToClipboard(eff.selectionText)
	}
	// Render only when a handler changed visible state: inert pointer motion
	// (mode 1003h reports all motion) must not repaint the transcript.
	if eff.render {
		t.RequestRender()
	}
}

// handleRightClickPaste pastes on an unmodified secondary press on Windows
// outside VS Code. Mirrors upstream handleRightClickPaste.
func (t *TuiAltScreen) handleRightClickPaste(event sgrMouseEvent) bool {
	if t.onRightClickPaste == nil || altScreenPlatform != "windows" ||
		strings.ToLower(os.Getenv("TERM_PROGRAM")) == "vscode" || event.release || event.button != 2 {
		return false
	}
	func() {
		// Clipboard paste is best-effort.
		defer func() { _ = recover() }() // upstream: tui/src/tui-alt-screen.ts:onRightClickPaste
		t.onRightClickPaste()
	}()
	return true
}

// handleScrollToEndIndicatorMouseEvent jumps to the end on a primary press
// on the indicator label. Mirrors upstream
// handleScrollToEndIndicatorMouseEvent.
func (t *TuiAltScreen) handleScrollToEndIndicatorMouseEvent(event sgrMouseEvent) bool {
	t.mu.Lock()
	rect := t.scrollToEndIndicatorRect
	t.mu.Unlock()
	if rect == nil || event.release || event.button&32 != 0 || event.button&3 != 0 {
		return false
	}
	if event.y != rect.row || event.x < rect.column || event.x >= rect.column+rect.width {
		return false
	}
	t.ScrollToBottom()
	return true
}

func (t *TuiAltScreen) getWheelScrollLines(button int) int {
	// SGR mouse button codes use bit 3 (value 8) for the Alt modifier.
	if button&8 != 0 {
		return t.wheelScrollLines * altWheelScrollMultiplier
	}
	return t.wheelScrollLines
}

// handleWheel offers a wheel event to mouse-aware components, then scrolls.
// It returns false to defer the raw report to a focused overlay. Mirrors the
// wheel branch of upstream handleViewportInput.
func (t *TuiAltScreen) handleWheel(wheel wheelEvent) bool {
	event := t.createMouseEvent(MouseWheel, wheel.button, wheel.x, wheel.y, wheel.direction*t.getWheelScrollLines(wheel.button), 0)
	if result := t.dispatchMouseToScreen(event); result != nil {
		if t.applyMouseDispatchResult(event, result) {
			t.RequestRender()
		}
		return true
	}
	if t.shouldDeferViewportInputToOverlay() {
		return false
	}
	t.routeWheel(wheel)
	return true
}

// routeWheel scrolls the scroll views under the pointer, chaining leftover
// delta outward and finally to the primary view. Mirrors upstream routeWheel.
func (t *TuiAltScreen) routeWheel(wheel wheelEvent) {
	t.mu.Lock()
	layout := t.currentLayout
	primary := t.getPrimaryScrollView()
	t.mu.Unlock()

	remaining := wheel.direction * t.getWheelScrollLines(wheel.button)
	seen := map[*ScrollView]bool{}
	if layout != nil {
		for _, sv := range GetScrollViewsAt(*layout, wheel.x, wheel.y) {
			seen[sv] = true
			remaining = sv.ScrollBy(remaining)
			if remaining == 0 || sv.Overscroll() == "contain" {
				break
			}
		}
	}
	if remaining != 0 && !seen[primary] {
		primary.ScrollBy(remaining)
	}
	t.mu.Lock()
	t.updateScrollbarHover(wheel.x, wheel.y)
	t.unlockAndApplyHover()
	t.RequestRender()
}

// getScrollbarTargetAt returns the scroll view whose scrollbar track covers
// (x, y). Caller holds t.mu. Mirrors upstream getScrollbarTargetAt.
func (t *TuiAltScreen) getScrollbarTargetAt(x, y int, includeHiddenAuto bool) (*ScrollView, ScrollbarGeometry, bool) {
	if t.hasOverlay() || t.currentLayout == nil {
		return nil, ScrollbarGeometry{}, false
	}
	for _, sv := range GetScrollViewsAt(*t.currentLayout, x, y) {
		box := GetScrollViewBox(*t.currentLayout, sv)
		if box == nil {
			continue
		}
		geometry := getScrollbarGeometry(box, includeHiddenAuto)
		if geometry != nil && x == geometry.Column && y >= geometry.TrackTop && y < geometry.TrackTop+geometry.TrackHeight {
			return sv, *geometry, true
		}
	}
	return nil, ScrollbarGeometry{}, false
}

// setScrollbarHover moves scrollbar-active state to scrollView (nil clears
// it), queued for unlockAndApplyHover. Caller holds t.mu.
func (t *TuiAltScreen) setScrollbarHover(scrollView *ScrollView) {
	if scrollView == t.scrollbarHover {
		return
	}
	if t.scrollbarHover != nil {
		t.pendingScrollbarActivity = append(t.pendingScrollbarActivity, scrollbarActivity{t.scrollbarHover, false})
	}
	t.scrollbarHover = scrollView
	if t.scrollbarHover != nil {
		t.pendingScrollbarActivity = append(t.pendingScrollbarActivity, scrollbarActivity{t.scrollbarHover, true})
	}
}

// updateScrollbarHover hovers the scrollbar track under (x, y), revealing a
// hidden auto scrollbar. Caller holds t.mu.
func (t *TuiAltScreen) updateScrollbarHover(x, y int) {
	sv, _, ok := t.getScrollbarTargetAt(x, y, true)
	if !ok {
		sv = nil
	}
	t.setScrollbarHover(sv)
}

func (t *TuiAltScreen) stopScrollbarHover() { t.setScrollbarHover(nil) }

func (t *TuiAltScreen) stopScrollbarDrag() { t.scrollbarDrag = nil }

// scrollbarScrollTop maps a pointer row to the scroll offset that puts the
// thumb under it. Mirrors upstream scrollScrollbarToPointer.
func scrollbarScrollTop(geometry ScrollbarGeometry, pointerY, grabOffset int) int {
	maxThumbOffset := geometry.TrackHeight - geometry.ThumbHeight
	thumbOffset := max(0, min(maxThumbOffset, pointerY-geometry.TrackTop-grabOffset))
	if maxThumbOffset == 0 {
		return 0
	}
	return int(math.Round(float64(thumbOffset) / float64(maxThumbOffset) * float64(geometry.MaxScrollTop)))
}

// handleScrollbarMouseEvent handles scrollbar presses (on the thumb, or on the
// track, which jumps there) and drags. Scrolling lands in eff. Caller holds
// t.mu. Mirrors upstream handleScrollbarMouseEvent.
func (t *TuiAltScreen) handleScrollbarMouseEvent(event sgrMouseEvent, eff *mouseEffects) bool {
	if t.scrollbarDrag != nil {
		if event.release {
			t.stopScrollbarDrag()
			return true
		}
		if t.currentLayout != nil {
			if box := GetScrollViewBox(*t.currentLayout, t.scrollbarDrag.scrollView); box != nil {
				if geometry := GetScrollbarGeometry(box); geometry != nil {
					eff.scrollSV = t.scrollbarDrag.scrollView
					eff.scrollTop = scrollbarScrollTop(*geometry, event.y, t.scrollbarDrag.grabOffset)
					eff.doScrollTo = true
				}
			}
		}
		return true
	}
	if event.release || event.button&32 != 0 || event.button&3 != 0 {
		return false
	}
	sv, geometry, ok := t.getScrollbarTargetAt(event.x, event.y, false)
	if !ok {
		return false
	}
	t.clearTextSelectionLocked()
	t.lastClick = nil
	t.setScrollbarHover(sv)
	onThumb := event.y >= geometry.ThumbTop && event.y < geometry.ThumbTop+geometry.ThumbHeight
	grabOffset := geometry.ThumbHeight / 2
	if onThumb {
		grabOffset = event.y - geometry.ThumbTop
	} else {
		eff.scrollSV = sv
		eff.scrollTop = scrollbarScrollTop(geometry, event.y, grabOffset)
		eff.doScrollTo = true
	}
	t.scrollbarDrag = &scrollbarDrag{scrollView: sv, grabOffset: grabOffset}
	return true
}

// safeOpenURL activates a clicked OSC 8 link, swallowing any panic from the
// host-provided opener (URL activation is best-effort).
func (t *TuiAltScreen) safeOpenURL(url string) {
	defer func() { _ = recover() }() // upstream: tui/src/tui-alt-screen.ts:openUrl
	t.openURL(url)
}

// compositeScrollToEndIndicator centers the jump-to-end label on the last row
// of a follow-end primary view scrolled away from its end, never over the
// scrollbar. Caller holds t.mu. Mirrors upstream
// compositeScrollToEndIndicator.
func (t *TuiAltScreen) compositeScrollToEndIndicator(screen []string, layout *LayoutFrame, width int) []string {
	t.scrollToEndIndicatorRect = nil
	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = t.implicitScrollView
	}
	if t.scrollToEndIndicator == nil || !scrollView.FollowEnd() || scrollView.IsFollowingEnd() {
		return screen
	}
	box := GetScrollViewBox(*layout, scrollView)
	if box == nil || box.Clip.Width <= 0 || box.Clip.Height <= 0 {
		return screen
	}
	clip := box.Clip
	row := clip.Y + clip.Height - 1
	if row >= len(screen) || widthx.IsImageLine(screen[row]) {
		return screen
	}
	label := widthx.TruncateToWidth(t.scrollToEndIndicator(), clip.Width, "", false)
	column := clip.X + (clip.Width-widthx.VisibleWidth(label))/2
	rightEdge := clip.X + clip.Width
	if geometry := GetScrollbarGeometry(box); geometry != nil {
		rightEdge = geometry.Column
	}
	text := widthx.TruncateToWidth(label, max(0, rightEdge-column), "", false)
	textWidth := widthx.VisibleWidth(text)
	if textWidth == 0 {
		return screen
	}
	result := slices.Clone(screen)
	result[row] = compositeTuiLine(result[row], text, column, textWidth, width)
	t.scrollToEndIndicatorRect = &scrollToEndIndicatorRect{row: row, column: column, width: textWidth}
	return result
}
