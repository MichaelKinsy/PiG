package widthx

import "testing"

// .upstream/v1.0.0/packages/tui/test/regression-slice-by-column-ansi-order.test.ts:6 (#10169).
func TestUpstreamSliceByColumnANSIOrderRegression(t *testing.T) {
	// regression-slice-by-column-ansi-order.test.ts:7.
	t.Run("keeps a reset at the slice start after earlier style codes", func(t *testing.T) {
		line := "\x1b[32mfoo\x1b[39m bar"
		if got, want := SliceByColumn(line, 3, 4, true), "\x1b[32m\x1b[39m bar"; got != want {
			t.Fatalf("SliceByColumn = %q, want %q", got, want)
		}
	})
	// regression-slice-by-column-ansi-order.test.ts:12.
	t.Run("does not leak color into text after a highlighted token", func(t *testing.T) {
		line := "Another \x1b[35malpha\x1b[39m line with \x1b[35mbeta\x1b[39m later."
		after := SliceByColumn(line, 13, 100, true)
		if want := "\x1b[35m\x1b[39m line with \x1b[35mbeta\x1b[39m later."; after != want {
			t.Fatalf("SliceByColumn = %q, want %q", after, want)
		}
	})
}
