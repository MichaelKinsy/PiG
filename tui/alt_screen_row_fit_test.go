package tui

import (
	"io"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type fixedLines []string

func (l *fixedLines) Render(int) []string { return slices.Clone(*l) }
func (l *fixedLines) Invalidate()         {}

// Every row a fullscreen frame keeps fits the width, whether it is new or equal to the row the frame before kept: a stream of
// frames that change some rows and leave others (wide ones included) ends with the screen a new renderer draws from the last
// frame alone.
func TestAltScreenKeptRowsFitTheWidthAcrossFrames(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	words := []string{"alpha", "\x1b[31mred\x1b[0m", "日本語", "😀", "wide-word-that-is-long", "\x1b]8;;http://x.y\x07link\x1b]8;;\x07", " "}
	row := func(width int) string {
		var line strings.Builder
		for line.Len() < width*(1+rng.IntN(3)) {
			line.WriteString(words[rng.IntN(len(words))])
		}
		return line.String()
	}
	for trial := range 40 {
		width, height := 10+rng.IntN(30), 4+rng.IntN(6)
		lines := &fixedLines{}
		ui := NewTuiAltScreenWithOutput(io.Discard, width, height, TuiAltScreenOptions{})
		ui.Start()
		t.Cleanup(ui.Stop)
		ui.Add(lines)
		rows := make([]string, height)
		for i := range rows {
			rows[i] = row(width)
		}
		for frame := range 12 {
			for range 1 + rng.IntN(2) {
				rows[rng.IntN(height)] = row(width)
			}
			if frame == 6 {
				// A narrower screen: rows equal to the last frame's were cut to the old width.
				width = max(6, width-3-rng.IntN(4))
				ui.SetFixedSize(width, height)
			}
			*lines = slices.Clone(rows)
			ui.Render()
			fresh := NewTuiAltScreenWithOutput(io.Discard, width, height, TuiAltScreenOptions{})
			fresh.Start()
			t.Cleanup(fresh.Stop)
			freshLines := fixedLines(slices.Clone(rows))
			fresh.Add(&freshLines)
			fresh.Render()
			if !slices.Equal(ui.previousScreen, fresh.previousScreen) {
				t.Fatalf("trial %d frame %d: kept screen differs from a new renderer's:\n got %q\nwant %q", trial, frame, ui.previousScreen, fresh.previousScreen)
			}
			for i, line := range ui.previousScreen {
				if widthx.VisibleWidth(line) > width {
					t.Fatalf("trial %d frame %d row %d is %d columns wide at width %d: %q", trial, frame, i, widthx.VisibleWidth(line), width, line)
				}
			}
		}
	}
}
