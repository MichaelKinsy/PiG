package tui

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi editor.ts:73-94 retains each original segment index even when a grapheme straddles the marker's end.
func TestEditorMarkerSegmentationRetainsSourceOffsets(t *testing.T) {
	e := NewEditor()
	upstreamPasteWithMarker(e)
	marker := upstreamMarker(t, e.Text())
	text := "😀" + marker + "\u0301Z"
	segments := e.segmentLine(text)
	last := segments[len(segments)-1]
	length := len(utf16.Encode([]rune(text)))
	if last.Text != "Z" || last.Start != length-1 || last.End != length {
		t.Fatalf("last segment=%+v, want Z at original UTF-16 offset %d", last, length-1)
	}
}

// Pi editor.ts:605-612,681-689 uses the registered marker-aware graphemes for both paint and hit testing.
func TestEditorMarkerCursorPaintAndMouseUseAtomicSegments(t *testing.T) {
	e := NewEditor()
	e.HandleInput("A")
	upstreamPasteWithMarker(e)
	e.HandleInput("B")
	marker := upstreamMarker(t, e.Text())
	e.HandleInput("\x01")
	e.HandleInput("\x1b[C")
	rows := e.Render(80)
	t.Run("paint", func(t *testing.T) {
		if !strings.Contains(rows[1], "A\x1b[7m"+marker+"\x1b[0mB") {
			t.Fatalf("cursor does not highlight the complete marker: %q", rows[1])
		}
	})
	t.Run("mouse", func(t *testing.T) {
		e.HandleMouse(TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft, X: 10, Y: 1, Width: 80})
		assertEditorUnitCursor(t, e, 0, 1)
	})
}

// Pi editor.ts:1980-2003,2166-2189 resets the last action and calls setCursorCol for word movement.
func TestEditorWordMovementResetsKillAndStickyState(t *testing.T) {
	t.Run("kill chain", func(t *testing.T) {
		e := NewEditor()
		e.SetText("first second")
		for _, key := range []string{"\x17", "\x1b[1;5D", "\x1b[1;5C", "\x17", "\x19"} {
			e.HandleInput(key)
		}
		assertEditorText(t, e, "first ")
	})
	t.Run("clamped sticky column", func(t *testing.T) {
		e := NewEditor()
		e.SetText("abcdefghij\nword\nabcdefghij")
		e.HandleInput("\x01")
		for range 8 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 1, 4)
		e.HandleInput("\x1b[1;5D")
		e.HandleInput("\x1b[1;5C")
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 4)
	})
}

func TestEditorWordDeleteUsesRegisteredAtomicSegments(t *testing.T) {
	for _, key := range []string{"\x17", "\x1bd"} {
		t.Run(key, func(t *testing.T) {
			e := NewEditor()
			e.HandleInput("学生")
			upstreamPasteWithMarker(e)
			marker := upstreamMarker(t, e.Text())
			if key == "\x1bd" {
				e.HandleInput("\x01")
				e.HandleInput("\x1b[1;5C")
			}
			e.HandleInput(key)
			assertEditorText(t, e, "学生")
			e.HandleInput("\x19")
			assertEditorText(t, e, "学生"+marker)
		})
	}
}

// Pi editor.ts:1678-1775 exits history browsing before either word deletion, including no-op deletion at the boundary.
func TestEditorWordDeleteExitsHistory(t *testing.T) {
	for _, key := range []string{"\x17", "\x1bd"} {
		t.Run(key, func(t *testing.T) {
			e := NewEditor()
			e.AddToHistory("older")
			e.AddToHistory("newer")
			e.HandleInput("\x1b[A")
			e.HandleInput(key)
			if key == "\x17" {
				e.HandleInput("\x1b[B")
			} else {
				e.HandleInput("\x1b[A")
			}
			assertEditorText(t, e, "newer")
		})
	}
}

func TestEditorCursorUsesUTF16Units(t *testing.T) {
	e := NewEditor()
	e.SetText("学生😀")
	assertEditorUnitCursor(t, e, 0, 4)
	e.HandleInput("\x1b[D")
	assertEditorUnitCursor(t, e, 0, 2)
}

func TestEditorWordSegmentsPreserveMetadata(t *testing.T) {
	e := NewEditor()
	upstreamPasteWithMarker(e)
	marker := upstreamMarker(t, e.Text())
	text := "😀学生" + marker + " hello"
	got := slices.Collect(e.segment(text, "word"))
	want := []SegmentData{
		{Segment: "😀", Index: 0, Input: text},
		{Segment: "学生", Index: 2, Input: text, IsWordLike: true},
		{Segment: marker, Index: 4, Input: text},
		{Segment: " ", Index: 4 + len(marker), Input: text},
		{Segment: "hello", Index: 5 + len(marker), Input: text, IsWordLike: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("segments=%+v, want %+v", got, want)
	}
}

func BenchmarkEditorWordAndMarkerInput(b *testing.B) {
	for _, tc := range []struct{ name, text string }{
		{"ordinary", "ordinary words and punctuation..."},
		{"CJK", "学生です你好，world世界"},
		{"mixed large", strings.Repeat("hello 世界 ", 128)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			e := NewEditor()
			e.SetText(tc.text)
			upstreamPasteWithMarker(e)
			e.SetMaxVisibleLines(7)
			b.ReportAllocs()
			for b.Loop() {
				e.HandleInput("\x1b[1;5D")
				e.Render(80)
				e.HandleInput("\x1b[1;5C")
				e.Render(80)
			}
		})
	}
}

func TestEditorMarkerRenderRetainsWidth(t *testing.T) {
	e := NewEditor()
	upstreamPasteWithMarker(e)
	e.HandleInput("\x01")
	for _, width := range []int{8, 20, 80} {
		for _, row := range e.Render(width) {
			if got := widthx.VisibleWidth(row); got != width {
				t.Fatalf("row width=%d, want %d: %q", got, width, row)
			}
		}
	}
}
