package widthx

import (
	"strings"
	"testing"
)

// BenchmarkApplyLineReset is the per-line cost of the line resets on the main-screen render path: a styled 80-column line without a marker (every
// line but the cursor's) and one with a stray marker.
func BenchmarkApplyLineReset(b *testing.B) {
	plain := "\x1b[31m" + strings.Repeat("hello world ", 6) + "\x1b[0m tail"
	marked := plain + CursorMarker
	b.Run("plain", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = ApplyLineReset(plain)
		}
	})
	b.Run("marker", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = ApplyLineReset(marked)
		}
	})
}
