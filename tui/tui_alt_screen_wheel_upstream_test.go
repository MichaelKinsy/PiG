package tui

import (
	"slices"
	"testing"
)

// .upstream/v0.99.2/packages/tui/test/tui-alt-screen.test.ts:507 (#9758: wheel line counts can change at runtime; Alt keeps its multiplier).
func TestUpstreamAltScreenAppliesRuntimeWheelLineCountUpdates(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{WheelScrollLines: WheelScrollLines{Lines: 3}})
	var deltas []int
	h.tui.Add(NewMouseRegion(NewText("wheel target"), func(event TuiMouseEvent) *TuiMouseEventResult {
		if event.Type != MouseWheel {
			return nil
		}
		deltas = append(deltas, event.WheelDelta)
		return &TuiMouseEventResult{Handled: true}
	}))
	h.start()
	h.send("\x1b[<64;1;1M")
	h.tui.SetWheelScrollLines(WheelScrollLines{Lines: 2})
	h.send("\x1b[<65;1;1M", "\x1b[<72;1;1M")
	if want := []int{-3, 2, -10}; !slices.Equal(deltas, want) {
		t.Fatalf("wheel deltas = %v, want %v", deltas, want)
	}
}
