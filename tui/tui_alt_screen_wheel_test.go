package tui

import (
	"slices"
	"testing"
)

// Auto mode reaches the renderer's wheel path: fast events accelerate, Alt keeps its multiplier over the accelerated count, and the delta a component sees is the delta the viewport scrolls by. .upstream/v0.99.1/packages/tui/src/tui-alt-screen.ts:690 (handleViewportInput wheel branch).
func TestAltScreenAutoWheelAcceleratesAndScrollsByTheDispatchedDelta(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{WheelScrollLines: WheelScrollLines{Auto: true}})
	h.tui.wheelScroll = NewWheelScrollAccelerator(WheelScrollLines{Auto: true}, true)
	now := 0.0
	previous := wheelClock
	wheelClock = func() float64 { return now }
	t.Cleanup(func() { wheelClock = previous })

	h.tui.Add(NewText(numberedLines(60)))
	h.start()
	top := h.tui.ViewportTop()
	// A wheel-up event 20 ms after the previous one moves five lines, as wheel-scroll.test.ts:38 expects.
	var moved []int
	for _, at := range []float64{0, 20, 40} {
		now = at
		before := h.tui.ViewportTop()
		h.send("\x1b[<64;1;1M")
		moved = append(moved, before-h.tui.ViewportTop())
	}
	if want := []int{1, 5, 5}; !slices.Equal(moved, want) {
		t.Fatalf("lines moved = %v, want %v (top started at %d)", moved, want, top)
	}
	// Alt+wheel multiplies the accelerated count.
	now = 60
	before := h.tui.ViewportTop()
	h.send("\x1b[<72;1;1M")
	if got := before - h.tui.ViewportTop(); got != 25 {
		t.Fatalf("alt wheel moved %d lines, want 25", got)
	}
}
