package tui

import (
	"io"
	"testing"
)

// BenchmarkAltScreenDoRender measures the fullscreen differential render path
// (the stakeholder's "at least as performant as pi" gate). It renders a
// steady-state frame (unchanged since the previous), so the differential
// row-skip is exercised, matching the common streaming case.
func BenchmarkAltScreenDoRender(b *testing.B) {
	tui := newAltScreenForTest(nil, 120, 40, TuiAltScreenOptions{})
	tui.out = io.Discard
	for i := range 200 {
		tui.Add(NewText("the quick brown fox jumps over the lazy dog: line filler content"))
		_ = i
	}
	tui.Start()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tui.doRender()
	}
}
