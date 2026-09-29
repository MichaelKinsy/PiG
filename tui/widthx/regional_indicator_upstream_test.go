package widthx

import "testing"

func TestUpstreamRegionalIndicatorWidth(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/regression-regional-indicator-width.test.ts:6
	t.Run("treats partial flag grapheme as full-width to avoid streaming render drift", func(t *testing.T) {
		if got := VisibleWidth("🇨"); got != 2 {
			t.Fatal(got)
		}
		if got := VisibleWidth("      - 🇨"); got != 10 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/regression-regional-indicator-width.test.ts:18
	t.Run("wraps intermediate partial-flag list line before overflow", func(t *testing.T) {
		got := WrapTextWithAnsi("      - 🇨", 9)
		if len(got) != 2 {
			t.Fatalf("rows = %q", got)
		}
		if VisibleWidth(got[0]) != 7 || VisibleWidth(got[1]) != 2 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/regression-regional-indicator-width.test.ts:28
	t.Run("treats all regional-indicator singleton graphemes as width 2", func(t *testing.T) {
		for cp := rune(0x1f1e6); cp <= 0x1f1ff; cp++ {
			if got := VisibleWidth(string(cp)); got != 2 {
				t.Errorf("%U width = %d, want 2", cp, got)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/regression-regional-indicator-width.test.ts:39
	t.Run("keeps full flag pairs at width 2", func(t *testing.T) {
		for _, sample := range []string{"🇯🇵", "🇺🇸", "🇬🇧", "🇨🇳", "🇩🇪", "🇫🇷"} {
			if got := VisibleWidth(sample); got != 2 {
				t.Errorf("%q width = %d, want 2", sample, got)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/regression-regional-indicator-width.test.ts:46
	t.Run("keeps common streaming emoji intermediates at stable width", func(t *testing.T) {
		for _, sample := range []string{"👍", "👍🏻", "✅", "⚡", "⚡️", "👨", "👨‍💻", "🏳️‍🌈"} {
			if got := VisibleWidth(sample); got != 2 {
				t.Errorf("%q width = %d, want 2", sample, got)
			}
		}
	})
}
