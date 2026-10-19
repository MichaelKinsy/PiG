package tui

import (
	"io"
	"testing"
)

// tui.ts OverlayHandle.getBounds: the most recent rendered bounds of that overlay, not of another visible one, and none once the overlay is gone.
func TestOverlayHandleGetBoundsReportsItsOwnOverlay(t *testing.T) {
	tu := newManualRenderTUI(io.Discard, 30, 10)
	first := tu.ShowOverlay(&mouseProbe{lines: []string{"aa"}}, OverlayOptions{width: overlayCells(4), anchor: overlayTopLeft, row: overlayCells(1), col: overlayCells(2)})
	second := tu.ShowOverlay(&mouseProbe{lines: []string{"b", "b", "b"}}, OverlayOptions{width: overlayCells(7), anchor: overlayTopLeft, row: overlayCells(5), col: overlayCells(9)})
	tu.composeOverlayLines(make([]string, 10), 30, 10)
	for _, tc := range []struct {
		name   string
		handle *OverlayHandle
		want   OverlayBounds
	}{
		{"first", first, OverlayBounds{Row: 1, Col: 2, Width: 4, Height: 1}},
		{"second", second, OverlayBounds{Row: 5, Col: 9, Width: 7, Height: 3}},
	} {
		if got, ok := tc.handle.GetBounds(); !ok || got != tc.want {
			t.Errorf("%s overlay: GetBounds = %+v %v, want %+v", tc.name, got, ok, tc.want)
		}
	}
	first.Close()
	if got, ok := first.GetBounds(); ok {
		t.Errorf("a closed overlay reports bounds %+v", got)
	}
	tu.composeOverlayLines(make([]string, 10), 30, 10)
	if got, ok := second.GetBounds(); !ok || got.Row != 5 {
		t.Errorf("the remaining overlay lost its bounds: %+v %v", got, ok)
	}
}
