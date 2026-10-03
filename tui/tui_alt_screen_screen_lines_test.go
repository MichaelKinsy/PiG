package tui

import (
	"bytes"
	"strings"
	"testing"
)

// tui-alt-screen.ts:315-318 (Pi 1.0.0): getScreenLines returns a copy of the last rendered frame, one line per terminal row, as written to the terminal. Upstream has no test for it; the case asserts the contract its doc comment states.
func TestAltScreenGetScreenLinesReturnsTheLastFrame(t *testing.T) {
	var out bytes.Buffer
	width, height := 20, 4
	screen := newAltScreenForTest(&out, width, height, TuiAltScreenOptions{})
	if lines := screen.GetScreenLines(); len(lines) != 0 {
		t.Fatalf("before the first frame: lines = %q, want none", lines)
	}
	first := NewText("line-a")
	screen.Add(first)
	screen.Add(NewText("line-b"))
	screen.Start()
	defer screen.StopWithOptions(StopOptions{PreserveScreen: true})

	lines := screen.GetScreenLines()
	if len(lines) != height {
		t.Fatalf("lines = %q, want %d rows", lines, height)
	}
	if !strings.Contains(lines[0], "line-a") || !strings.Contains(lines[1], "line-b") {
		t.Fatalf("lines = %q, want the rendered text rows", lines)
	}
	// The rows are what the renderer wrote: each painted row appears in the terminal output.
	for row, line := range lines {
		if line != "" && !strings.Contains(out.String(), line) {
			t.Fatalf("row %d %q was not written to the terminal", row, line)
		}
	}

	lines[0] = "mutated"
	if got := screen.GetScreenLines()[0]; got == "mutated" {
		t.Fatal("GetScreenLines returned the renderer's own slice")
	}

	first.Content = "line-A"
	first.Invalidate()
	screen.Render()
	if got := screen.GetScreenLines()[0]; !strings.Contains(got, "line-A") {
		t.Fatalf("after a re-render: first line = %q, want line-A", got)
	}
}
