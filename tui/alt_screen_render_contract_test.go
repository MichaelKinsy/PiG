package tui

// pi: packages/tui/src/tui-alt-screen.ts doRender (1693-1799), render (335-337), implicitDocument.render (264); packages/tui/src/layout.ts renderLayoutFrame (383-412)

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// A viewport of zero or negative columns or rows is laid out as one cell, and the frame draws that one row.
func TestRenderLayoutFrameClampsTheViewportToOneCell(t *testing.T) {
	for _, size := range [][2]int{{0, 0}, {-3, 5}, {7, -1}, {0, 4}} {
		frame := RenderLayoutFrame(NewText("x"), size[0], size[1], nil)
		wantWidth, wantHeight := max(1, size[0]), max(1, size[1])
		if frame.Width != wantWidth || frame.Height != wantHeight || len(frame.Lines) != wantHeight {
			t.Errorf("viewport %v: frame %dx%d with %d lines, want %dx%d", size, frame.Width, frame.Height, len(frame.Lines), wantWidth, wantHeight)
		}
	}
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 0, 0, TuiAltScreenOptions{})
	tui.Add(NewText("x"))
	tui.Start()
	tui.Render()
	if got := out.String(); !strings.Contains(got, "\x1b[1;1H") || strings.Contains(got, "\x1b[2;1H") {
		t.Fatalf("a 0x0 terminal draws one row: %q", got)
	}
}

// The frame drops the OSC 133 zone prefix a line starts with, and so does the document dumped on exit; the frame's layout lines keep it for prompt jumps.
func TestAltScreenFrameAndExitDumpDropZonePrefixes(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 4, TuiAltScreenOptions{})
	tui.Add(&inputRecorder{lines: []string{"\x1b]133;A\x07hello"}})
	tui.Start()
	tui.Render()
	if got := out.String(); !strings.Contains(got, "hello") || strings.Contains(got, "\x1b]133;A") {
		t.Fatalf("frame = %q, want the text without its zone prefix", got)
	}
	out.Reset()
	tui.Stop()
	if got := out.String(); !strings.Contains(got, "hello") || strings.Contains(got, "\x1b]133;A") {
		t.Fatalf("exit dump = %q, want the text without its zone prefix", got)
	}
}

// A screen that has not started, or has stopped, draws nothing when asked to.
func TestAltScreenDoesNotDrawWhileInactive(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 4, TuiAltScreenOptions{})
	tui.Add(NewText("hello"))
	tui.doRender()
	if out.Len() != 0 {
		t.Fatalf("a screen that has not started wrote %q", out.String())
	}
	tui.Start()
	tui.Render()
	tui.Stop()
	out.Reset()
	tui.doRender()
	if out.Len() != 0 {
		t.Fatalf("a stopped screen wrote %q", out.String())
	}
}

// The implicit document the primary scroll view holds renders the screen's own children.
func TestAltScreenImplicitDocumentRendersTheScreensChildren(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 4, TuiAltScreenOptions{})
	tui.Add(NewText("first"))
	tui.Add(NewText("second"))
	want := []string{"first", "second"}
	if got := plainLines(tui.implicitDocument.Render(20)); !slices.Equal(got, want) {
		t.Fatalf("document = %q, want %q", got, want)
	}
}
