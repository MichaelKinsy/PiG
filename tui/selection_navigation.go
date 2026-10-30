package tui

import "slices"

// GetSelectPageIndex is getSelectPageIndex of packages/tui/src/keybindings.ts (Pi 3bae8527): the index a
// tui.select.pageUp or tui.select.pageDown key moves a selection to, moved by pageSize and clamped to the list without
// wrapping, and false when data is neither key. It returns 0 for a page key on an empty list, so callers must still check
// that the list has items before selecting.
//
// pig divergence (D155): Pi 1.1.0 handles these keys in only some selection lists (Pi #7629).
// pig divergence (D217): Pi 3bae8527 (#7629) merged form of D155, named getSelectPageIndex and capturesViewportInput.
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
// (packages/tui/src/tui.ts, Pi 3bae8527): while focused, fullscreen viewport keys (the tui.altScreen.* scrolling and prompt
// jumps, tui.select.pageUp and tui.select.pageDown among them) go to the component instead of scrolling the transcript, as
// they do for a focused overlay.
type ViewportInputCapturer interface {
	CapturesViewportInput() bool
}

// CapturesViewportInput reports whether component implements ViewportInputCapturer and captures viewport input. It is false for a nil component.
func CapturesViewportInput(component Component) bool {
	capturer, ok := component.(ViewportInputCapturer)
	return ok && capturer.CapturesViewportInput()
}

// CapturesViewportInput reports whether any child captures viewport input. Pi's Container has no such property; PiG keeps this
// aggregation of D155 so a wrapper of a capturing list captures without repeating the flag.
func (c *Container) CapturesViewportInput() bool {
	c.mu.RLock()
	children := append([]Component(nil), c.children...)
	c.mu.RUnlock()
	return slices.ContainsFunc(children, CapturesViewportInput)
}

// pageSelectionKey reports whether data is a selection page key.
func pageSelectionKey(kb *TUIKeybindingsManager, data string) bool {
	return kb.Matches(data, KBSelectPageUp) || kb.Matches(data, KBSelectPageDown)
}
