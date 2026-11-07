//go:build !race

package tui

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// typingAltFrame renders fullscreen frames as typing into the editor does: the prompt row changes each frame. With
// withCursor the row draws its cursor cell as a fake cursor (the editor's renderFakeCursor); without, the same cell is plain
// text. The rows are built before measuring, so a frame's allocations are the screen's own.
func typingAltFrame(tb testing.TB, withCursor bool) (step func()) {
	tb.Helper()
	out := &bytes.Buffer{}
	alt := newAltScreenForTest(out, 160, 48, TuiAltScreenOptions{})
	for i := range 30 {
		alt.Add(NewText(fmt.Sprintf("transcript row %d: %s", i, strings.Repeat("settled words ", 8))))
	}
	prompt := NewText("")
	alt.Add(prompt)
	alt.Add(NewText("footer: model, tokens and cwd"))
	rows := make([]string, 40)
	for i := range rows {
		cursor := " "
		if withCursor {
			cursor = widthx.RenderFakeCursor(" ")
		}
		rows[i] = "> " + strings.Repeat("abcdefghij", 4)[:i] + cursor
	}
	alt.Start()
	n := 0
	return func() {
		n++
		prompt.SetText(rows[n%len(rows)])
		out.Reset()
		alt.Render()
	}
}

func minAllocsPerFrame(step func()) float64 {
	for range 50 {
		step()
	}
	// Allocations of goroutines other tests left running only add to a run, so the least of several runs is the frame's own count.
	got := math.MaxFloat64
	for range 5 {
		got = min(got, testing.AllocsPerRun(100, step))
	}
	return got
}

// A keystroke frame whose prompt row holds the editor's fake cursor costs one allocation more than the same frame drawn
// without one: the resolved row (tui-alt-screen.ts doRender resolveFakeCursors). The screen the frame built is its own, so
// resolving it neither copies the frame nor makes a string per marker kind.
func TestAltScreenFakeCursorCostsOneAllocationPerFrame(t *testing.T) {
	plain := minAllocsPerFrame(typingAltFrame(t, false))
	cursor := minAllocsPerFrame(typingAltFrame(t, true))
	if extra := cursor - plain; extra > 1 {
		t.Fatalf("a frame with a fake cursor allocates %.1f times, %.1f more than without one (%.1f); want at most 1 more", cursor, extra, plain)
	}
}
