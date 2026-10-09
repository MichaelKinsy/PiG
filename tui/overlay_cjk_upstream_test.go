package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestUpstreamOverlayCJKBoundary(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/regression-overlay-cjk-boundary.test.ts:7
	t.Run("excludes a wide grapheme from before when overlay starts inside it", func(t *testing.T) {
		got := widthx.ExtractSegments("abcd让EFGH", 5, 9, 11, true)
		if got.Before != "abcd" || got.BeforeWidth != 4 || widthx.VisibleWidth(got.Before) != got.BeforeWidth || got.After != "H" || got.AfterWidth != 1 {
			t.Fatalf("segments = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/regression-overlay-cjk-boundary.test.ts:17
	t.Run("keeps ASCII before-segment behavior at the same boundary", func(t *testing.T) {
		got := widthx.ExtractSegments("abcdG EFGH", 5, 9, 11, true)
		if got.Before != "abcdG" || got.BeforeWidth != 5 || widthx.VisibleWidth(got.Before) != got.BeforeWidth {
			t.Fatalf("segments = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/regression-overlay-cjk-boundary.test.ts:25
	t.Run("composites an overlay at the requested column when it starts inside a wide grapheme", func(t *testing.T) {
		got := CompositeTuiLine("abcd让EFGH", "│XX│", 5, 4, 20)
		prefix, overlay := widthx.SliceByColumn(got, 0, 5, true), widthx.SliceByColumn(got, 5, 4, true)
		if strings.Contains(got, "让") || widthx.VisibleWidth(got) != 20 || widthx.VisibleWidth(prefix) != 5 || widthx.VisibleWidth(overlay) != 4 || !strings.Contains(overlay, "│XX│") {
			t.Fatalf("line=%q prefix=%q overlay=%q", got, prefix, overlay)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/regression-overlay-cjk-boundary.test.ts:37
	t.Run("composites an overlay when it starts at a wide grapheme boundary", func(t *testing.T) {
		got := CompositeTuiLine("abcd让EFGH", "│XX│", 4, 4, 20)
		overlay := widthx.SliceByColumn(got, 4, 4, true)
		if strings.Contains(got, "让") || widthx.VisibleWidth(got) != 20 || widthx.VisibleWidth(overlay) != 4 || !strings.Contains(overlay, "│XX│") {
			t.Fatalf("line=%q overlay=%q", got, overlay)
		}
	})
}
