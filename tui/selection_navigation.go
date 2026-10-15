package tui

import "slices"

// PageSelectionIndex is getPageSelectionIndex of Pi PR #7680 (packages/tui/src/selection-navigation.ts): the index a
// tui.select.pageUp or tui.select.pageDown key moves a selection to, clamped to the list without wrapping, and false
// when data is neither key.
//
// pig divergence (D155): Pi 1.1.0 handles these keys in only some selection lists (Pi #7629).
func PageSelectionIndex(kb *TUIKeybindingsManager, data string, selectedIndex, itemCount, pageSize int) (int, bool) {
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

// SelectPageInputCapturer is implemented by components whose selection handles tui.select.pageUp and
// tui.select.pageDown. Fullscreen mode gives those keys to the focused capturer before it scrolls the viewport. Mirrors
// Pi PR #7680's Component.capturesSelectPageInput.
type SelectPageInputCapturer interface {
	CapturesSelectPageInput() bool
}

// CapturesSelectPageInput reports whether component handles selection page keys.
func CapturesSelectPageInput(component Component) bool {
	capturer, ok := component.(SelectPageInputCapturer)
	return ok && capturer.CapturesSelectPageInput()
}

// CapturesSelectPageInput reports whether any child captures selection page keys, as Pi's Container does.
func (c *Container) CapturesSelectPageInput() bool {
	c.mu.RLock()
	children := append([]Component(nil), c.children...)
	c.mu.RUnlock()
	return slices.ContainsFunc(children, CapturesSelectPageInput)
}

// pageSelectionKey reports whether data is a selection page key.
func pageSelectionKey(kb *TUIKeybindingsManager, data string) bool {
	return kb.Matches(data, KBSelectPageUp) || kb.Matches(data, KBSelectPageDown)
}
