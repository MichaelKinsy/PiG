package tui

import (
	"math"
	"testing"
)

func TestResolveOverlayLayoutPinnedBoundaries(t *testing.T) {
	anchors := []struct {
		name overlayAnchor
		row  int
		col  int
	}{
		{overlayTopLeft, 2, 3},
		{overlayTopCenter, 2, 35},
		{overlayTopRight, 2, 68},
		{overlayLeftCenter, 16, 3},
		{overlayCenter, 16, 35},
		{overlayRightCenter, 16, 68},
		{overlayBottomLeft, 30, 3},
		{overlayBottomCenter, 30, 35},
		{overlayBottomRight, 30, 68},
	}
	for _, tc := range anchors {
		t.Run(string(tc.name), func(t *testing.T) {
			layout := resolveOverlayLayout(OverlayOptions{
				width:  overlayCells(29),
				anchor: tc.name,
				margin: overlayMargin{Top: 2, Right: 3, Bottom: 2, Left: 3},
			}, 8, 100, 40)
			if layout.row != tc.row || layout.col != tc.col || layout.width != 29 {
				t.Fatalf("layout = %+v, want row=%d col=%d width=29", layout, tc.row, tc.col)
			}
		})
	}

	all := 4
	layout := resolveOverlayLayout(OverlayOptions{
		width:     overlayPercent(50),
		minWidth:  60,
		maxHeight: overlayPercent(25),
		row:       overlayPercent(100),
		col:       overlayPercent(100),
		offsetX:   99,
		offsetY:   99,
		marginAll: &all,
	}, 99, 100, 40)
	if layout.width != 60 || !layout.hasMaxHeight || layout.maxHeight != 10 || layout.row != 26 || layout.col != 36 {
		t.Fatalf("percentage/clamped boundary layout = %+v", layout)
	}

	invalid := resolveOverlayLayout(OverlayOptions{
		width:     overlaySize{value: math.NaN(), set: true},
		maxHeight: overlaySize{value: math.Inf(1), set: true},
		margin:    overlayMargin{Top: -5, Right: -5, Bottom: -5, Left: -5},
	}, 0, 0, 0)
	if invalid.width != 1 || invalid.row != 0 || invalid.col != 0 || invalid.hasMaxHeight {
		t.Fatalf("invalid/empty terminal fallback = %+v", invalid)
	}
}
