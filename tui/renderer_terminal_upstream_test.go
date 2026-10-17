package tui

import (
	"bytes"
	"testing"
)

// Ports tui.ts:497 `public terminal: Terminal` of TuiBase: both renderers expose the terminal they draw on, and it is one stable
// value. A renderer with a fixed output and size gets a terminal that writes control sequences to that output and reports that size
// (terminal.ts:488 columns/rows are the terminal's, here the renderer's).
func TestRendererTerminalDrawsOnTheRendererOutputAndSize(t *testing.T) {
	for name, build := range map[string]func(*bytes.Buffer) interface{ Terminal() Terminal }{
		"main": func(b *bytes.Buffer) interface{ Terminal() Terminal } { return NewWithOutput(b, 40, 10) },
		"alt": func(b *bytes.Buffer) interface{ Terminal() Terminal } {
			return NewTuiAltScreenWithOutput(b, 40, 10, TuiAltScreenOptions{})
		},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			renderer := build(&out)
			terminal := renderer.Terminal()
			if terminal == nil || renderer.Terminal() != terminal {
				t.Fatal("Terminal must return one stable value")
			}
			if terminal.Columns() != 40 || terminal.Rows() != 10 {
				t.Fatalf("size = %dx%d, want the renderer's 40x10", terminal.Columns(), terminal.Rows())
			}
			terminal.HideCursor()
			terminal.Write("x")
			if got := out.String(); got != "\x1b[?25lx" {
				t.Fatalf("terminal wrote %q to the renderer output, want %q", got, "\x1b[?25lx")
			}
		})
	}

	var viewport ViewportTUI = NewTuiAltScreenWithOutput(&bytes.Buffer{}, 7, 3, TuiAltScreenOptions{})
	if viewport.Terminal().Columns() != 7 {
		t.Fatal("ViewportTUI.Terminal does not report the renderer size")
	}
	resizing := NewWithOutput(&bytes.Buffer{}, 40, 10)
	terminal := resizing.Terminal()
	resizing.SetFixedSize(60, 20)
	if terminal.Columns() != 60 || terminal.Rows() != 20 {
		t.Fatalf("size after a resize = %dx%d, want 60x20", terminal.Columns(), terminal.Rows())
	}
}
