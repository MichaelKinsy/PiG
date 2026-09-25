package tui

import (
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestDrawBoxClipsColoredCellsWithoutDiscardingSGR(t *testing.T) {
	// Pi's compositeTuiLine uses ANSI-aware cell slicing when an overlay exceeds its width.
	const pixel = "\x1b[38;2;180;20;30m\x1b[48;2;10;40;90m▀"
	for _, tc := range []struct {
		name, line, visible string
	}{
		{name: "empty", visible: "    "},
		{name: "fits", line: pixel + "\x1b[0m", visible: "▀   "},
		{name: "overflow", line: strings.Repeat(pixel, 6) + "\x1b[0m", visible: "▀▀▀▀"},
		{name: "wide boundary", line: "\x1b[31mabc界界\x1b[0m", visible: "abc "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := drawBox("", []string{tc.line}, 6, 3)[1]
			if got := widthx.StripAnsi(row); got != "│"+tc.visible+"│" {
				t.Fatalf("visible cells = %q, want %q", got, "│"+tc.visible+"│")
			}
			if tc.line != "" {
				sequence := tc.line[:strings.Index(tc.line, "m")+1]
				if !strings.Contains(row, sequence) {
					t.Fatalf("clipping discarded SGR %q: %q", sequence, row)
				}
			}
		})
	}
}

func TestModalOverlayClipsRemoteFrameWithoutLosingColors(t *testing.T) {
	const pixel = "\x1b[38;2;180;20;30m\x1b[48;2;10;40;90m▀"
	regular := NewWithOutput(io.Discard, 20, 8)
	fullscreen := NewTuiAltScreenWithOutput(io.Discard, 20, 8, TuiAltScreenOptions{})
	for name, base := range map[string]*tuiBase{"regular": &regular.tuiBase, "fullscreen": &fullscreen.tuiBase} {
		t.Run(name, func(t *testing.T) {
			// Remote snapshots can arrive at terminal width before an overlay's narrower render width is known.
			base.OpenOverlay(&recordingComponent{lines: []string{strings.Repeat(pixel, 20) + "\x1b[0m"}}, OverlayOptions{
				WidthFraction: 0.75, HeightFraction: 0.7,
			})
			rows := base.composeOverlayLines(nil, 20, 8)
			if !strings.Contains(strings.Join(rows, "\n"), pixel) {
				t.Fatalf("modal compositor removed remote frame colors: %q", rows)
			}
			for _, row := range rows {
				if widthx.VisibleWidth(row) > 20 {
					t.Fatalf("modal row exceeds terminal width: %q", row)
				}
			}
		})
	}
}

func BenchmarkModalColoredClipping(b *testing.B) {
	pixel := "\x1b[38;2;180;20;30m\x1b[48;2;10;40;90m▀"
	lines := make([]string, 32)
	for i := range lines {
		lines[i] = strings.Repeat(pixel, 120) + "\x1b[0m"
	}
	ui := NewWithOutput(io.Discard, 120, 40)
	ui.OpenOverlay(&recordingComponent{lines: lines}, OverlayOptions{WidthFraction: 0.75, HeightFraction: 0.7})
	b.ReportAllocs()
	for b.Loop() {
		ui.composeOverlayLines(nil, 120, 40)
	}
}

func TestOverlayCompositorTerminalCellsRegularAndFullscreen(t *testing.T) {
	regular := NewWithOutput(io.Discard, 20, 8)
	fullscreen := NewTuiAltScreenWithOutput(io.Discard, 20, 8, TuiAltScreenOptions{})

	for name, base := range map[string]*tuiBase{
		"regular":    &regular.tuiBase,
		"fullscreen": &fullscreen.tuiBase,
	} {
		t.Run(name, func(t *testing.T) {
			image := "\x1b_Ga=T,f=100,i=7,r=1;AAAA\x1b\\"
			background := []string{
				"\x1b[31mAB界C\tD\x1b[0m",
				image,
				"0123456789abcdefghij",
			}
			lower := &recordingComponent{lines: []string{"\x1b[1;34mLOW界e\u0301\x1b[0m"}}
			front := &recordingComponent{lines: []string{
				"\x1b]8;;https://example.test\x07👩‍💻X" + widthx.CursorMarker + "YZ\x1b]8;;\x07",
			}}
			base.OpenOverlay(lower, OverlayOptions{width: overlayCells(10), anchor: overlayTopLeft, row: overlayCells(2)})
			base.OpenOverlay(front, OverlayOptions{width: overlayCells(8), anchor: overlayTopLeft, row: overlayCells(2), col: overlayCells(4)})

			got := base.composeOverlayLines(background, 20, 8)
			if got[1] != image {
				t.Fatalf("opaque image line changed:\n got %q\nwant %q", got[1], image)
			}
			for row, line := range got {
				if !utf8.ValidString(line) {
					t.Fatalf("row %d is invalid UTF-8: %q", row, line)
				}
				if widthx.VisibleWidth(line) > 20 {
					t.Fatalf("row %d width = %d > 20: %q", row, widthx.VisibleWidth(line), line)
				}
			}
			composed := got[2]
			for _, sequence := range []string{
				"\x1b[1;34m",
				"\x1b]8;;https://example.test\x07",
				"👩‍💻",
				widthx.CursorMarker,
				"\x1b]8;;\x07",
				widthx.SegmentReset,
			} {
				if !strings.Contains(composed, sequence) {
					t.Fatalf("composed row lost %q: %q", sequence, composed)
				}
			}
			cursorLines := append([]string(nil), got...)
			cursor, ok := widthx.ExtractCursorPosition(cursorLines, 8)
			// The front overlay starts at col 4. The lower overlay's 界 spans
			// cols 3-4, so upstream extractSegments leaves it out of "before"
			// and pads to col 4; the marker follows 👩‍💻 (2) and X (1) at col 7.
			if !ok || cursor.Row != 2 || cursor.Col != 7 {
				t.Fatalf("cursor = %+v, present=%v; want row=2 col=7", cursor, ok)
			}
			if strings.Contains(cursorLines[2], widthx.CursorMarker) {
				t.Fatal("cursor marker survived extraction")
			}
		})
	}
}
