package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/tui/termsim"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// .upstream/v0.87.1/packages/tui/test/overlay-short-content.test.ts:28
func TestUpstreamOverlayShortContent(t *testing.T) {
	t.Run("should render overlay when content is shorter than terminal height", func(t *testing.T) {
		var output bytes.Buffer
		ui := NewWithOutput(&output, 80, 24)
		ui.Add(&recordingComponent{lines: []string{"Line 1", "Line 2", "Line 3"}})
		ui.ShowOverlay(&recordingComponent{lines: []string{"OVERLAY_TOP", "OVERLAY_MID", "OVERLAY_BOT"}}, OverlayOptions{})
		ui.Render()
		terminal := termsim.New(24, 80)
		terminal.Write(output.Bytes())
		if !strings.Contains(terminal.String(), "OVERLAY") {
			t.Fatalf("overlay absent:\n%s", terminal.String())
		}
	})
}

// .upstream/v0.87.1/packages/tui/test/tui-shrink.test.ts:22
func TestUpstreamTUIShrink(t *testing.T) {
	t.Run("clears all rendered lines when content shrinks to zero", func(t *testing.T) {
		var output bytes.Buffer
		ui := NewWithOutput(&output, 40, 10)
		ui.Add(&recordingComponent{lines: []string{"first", "second", "third"}})
		terminal := termsim.New(10, 40)
		ui.Render()
		terminal.Write(output.Bytes())
		for _, text := range []string{"first", "second", "third"} {
			if !strings.Contains(terminal.String(), text) {
				t.Fatalf("%q absent:\n%s", text, terminal.String())
			}
		}
		output.Reset()
		ui.Clear()
		ui.Render()
		terminal.Write(output.Bytes())
		for _, text := range []string{"first", "second", "third"} {
			if strings.Contains(terminal.String(), text) {
				t.Fatalf("%q not cleared:\n%s", text, terminal.String())
			}
		}
	})
}

func TestUpstreamOverlayStyleLeak(t *testing.T) {
	for _, tt := range []struct {
		name    string
		overlay bool
	}{
		// .upstream/v0.87.1/packages/tui/test/tui-overlay-style-leak.test.ts:53
		{"should not leak styles when a trailing reset sits beyond the last visible column (no overlay)", false},
		// .upstream/v0.87.1/packages/tui/test/tui-overlay-style-leak.test.ts:66
		{"should not leak styles when overlay slicing drops trailing SGR resets", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			ui := NewWithOutput(&output, 20, 6)
			ui.Add(&recordingComponent{lines: []string{"\x1b[3m" + strings.Repeat("X", 20) + "\x1b[23m", "INPUT"}})
			if tt.overlay {
				ui.ShowOverlay(&recordingComponent{lines: []string{"OVR"}}, OverlayOptions{row: overlayCells(0), col: overlayCells(5), width: overlayCells(3)})
			}
			terminal := termsim.New(6, 20)
			ui.Render()
			terminal.Write(output.Bytes())
			output.Reset()
			ui.ForceFullRender()
			ui.Render()
			terminal.Write(output.Bytes())
			if terminal.CellAt(1, 0).Style.Italic {
				t.Fatalf("italic leaked into input row:\n%s", terminal.String())
			}
		})
	}
}

func TestUpstreamTabWidth(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/tab-width.test.ts:38
	t.Run("keeps slice helper widths consistent with visible width", func(t *testing.T) {
		got := widthx.SliceWithWidth("out 192M\t.pi/skill-tests/results-ha", 0, 10, true)
		if got.Text != "out 192M" || got.Width != 8 || widthx.VisibleWidth(got.Text) != got.Width {
			t.Fatalf("slice = %+v", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/tab-width.test.ts:47
	t.Run("keeps overlay segment widths consistent with visible width", func(t *testing.T) {
		text := "out 192M\t.pi/skill-tests/results-ha"
		got := widthx.ExtractSegments(text, 10, 13, 10, true)
		if got.Before != "out 192M" || got.BeforeWidth != 8 || widthx.VisibleWidth(got.Before) != got.BeforeWidth {
			t.Fatalf("segments = %+v", got)
		}
		fits := widthx.ExtractSegments(text, 11, 13, 10, true)
		if fits.Before != "out 192M\t" || fits.BeforeWidth != 11 || widthx.VisibleWidth(fits.Before) != fits.BeforeWidth {
			t.Fatalf("tab fits = %+v", fits)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/tab-width.test.ts:61
	t.Run("keeps tabs inside terminal control sequences byte-identical", func(t *testing.T) {
		for _, control := range []string{"\x1b]8;;https://example.test/a\tb\x07", "\x1b]0;window\ttitle\x1b\\", "\x1b_payload\tdata\x1b\\"} {
			if got := widthx.NormalizeTerminalOutput(control + "label\ttext"); got != control+"label   text" {
				t.Fatalf("normalized = %q", got)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/tab-width.test.ts:73
	t.Run("keeps tab-containing overlays on one physical terminal row", func(t *testing.T) {
		var output bytes.Buffer
		ui := NewWithOutput(&output, 16, 3)
		ui.Add(renderFuncComponent(func(width int) []string {
			var lines []string
			for _, s := range []string{"base 0", "base 1", "base 2"} {
				lines = append(lines, s+strings.Repeat(" ", width-len(s)))
			}
			return lines
		}))
		ui.ShowOverlay(&recordingComponent{lines: []string{"\tX"}}, OverlayOptions{width: overlayCells(4), row: overlayCells(1), col: overlayCells(4)})
		ui.Render()
		terminal := termsim.New(3, 16)
		terminal.Write(output.Bytes())
		var viewport strings.Builder
		for row := range 3 {
			if row > 0 {
				viewport.WriteByte('\n')
			}
			for col := range 16 {
				viewport.WriteRune(terminal.CellAt(row, col).Rune)
			}
		}
		if got, want := viewport.String(), "base 0          \nbase   X        \nbase 2          "; got != want {
			t.Fatalf("viewport = %q, want %q", got, want)
		}
		if strings.Contains(output.String(), "\t") {
			t.Fatalf("terminal output contains tab: %q", output.String())
		}
	})
}
