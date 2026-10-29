package tui

// GetSelectPageIndex is getSelectPageIndex of packages/tui/src/keybindings.ts: the index a tui.select.pageUp or
// tui.select.pageDown key moves a selection to, moved by pageSize and clamped to 0..itemCount-1 without wrapping. It
// reports false when data is neither key, and returns 0 for a page key on an empty list, so callers must still check
// that the list has items before selecting.
func GetSelectPageIndex(kb *TUIKeybindingsManager, data string, selectedIndex, itemCount, pageSize int) (int, bool) {
	direction := 0
	switch {
	case kb.Matches(data, KBSelectPageUp):
		direction = -1
	case kb.Matches(data, KBSelectPageDown):
		direction = 1
	default:
		return 0, false
	}
	return max(0, min(itemCount-1, selectedIndex+direction*pageSize)), true
}

// ViewportInputCapturer is implemented by components that mirror Pi's optional Component.capturesViewportInput
// (packages/tui/src/tui.ts): while focused, fullscreen viewport keys (tui.altScreen.* scrolling and prompt jumps) go to
// the component instead of scrolling the transcript, as they do for a focused overlay. Only the focused component is
// checked: a wrapper that forwards input to a list must report the list's value itself.
type ViewportInputCapturer interface {
	CapturesViewportInput() bool
}

// CapturesViewportInput reports whether c implements ViewportInputCapturer and captures viewport input. It is false for
// a nil component.
func CapturesViewportInput(c Component) bool {
	capturer, ok := c.(ViewportInputCapturer)
	return ok && capturer.CapturesViewportInput()
}
