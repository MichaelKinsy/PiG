package tui

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TestApplyLineResetsCached_MatchesPureAcrossFrames is the black-box oracle
// for the reuse cache: across a sequence of frames (unchanged prefix, changed
// tail, growth, shrink, image lines), the cached output must be byte-identical
// to widthx.ApplyLineResets computed fresh. The cache may only make it faster.
func TestApplyLineResetsCached_MatchesPureAcrossFrames(t *testing.T) {
	tui := &TUI{}

	frames := [][]string{
		{"alpha", "beta", "gamma"},
		{"alpha", "beta", "gamma DELTA"},            // tail changed
		{"alpha", "beta", "gamma DELTA", "epsilon"}, // grew
		{"alpha", "CHANGED", "gamma DELTA", "epsilon"},
		{"alpha", "CHANGED"},                       // shrank
		{"\x1b_Gf=100;imagepayload", "alpha", "z"}, // image line (no reset suffix)
		{},                              // empty
		{"alpha", "beta", "gamma"},      // back to start
		{"con\u0e33tains-lao", "plain"}, // NormalizeTerminalOutput path
	}

	for fi, f := range frames {
		got := tui.applyLineResetsCached(f)
		want := widthx.ApplyLineResets(f)
		if len(got) != len(want) {
			t.Fatalf("frame %d: len got=%d want=%d", fi, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("frame %d line %d:\n got=%q\nwant=%q", fi, i, got[i], want[i])
			}
		}
	}
}

// BenchmarkApplyLineResetsCached_TypingFrame models the hot case: an
// 8000-line transcript where a single line changes each frame (a keystroke
// updates the editor row). Compare to the pure ApplyLineResets (~3.6ms).
func BenchmarkApplyLineResetsCached_TypingFrame(b *testing.B) {
	const n = 8000
	base := make([]string, n)
	for i := range base {
		base[i] = fmt.Sprintf("line %d with some content and a bit of length to it", i)
	}
	tui := &TUI{}
	_ = tui.applyLineResetsCached(base) // warm
	b.ResetTimer()
	for k := range b.N {
		base[n-1] = fmt.Sprintf("editor row keystroke %d", k) // one line changes
		_ = tui.applyLineResetsCached(base)
	}
}

func BenchmarkApplyLineResets_Pure(b *testing.B) {
	const n = 8000
	base := make([]string, n)
	for i := range base {
		base[i] = fmt.Sprintf("line %d with some content and a bit of length to it", i)
	}
	b.ResetTimer()
	for range b.N {
		_ = widthx.ApplyLineResets(base)
	}
}
