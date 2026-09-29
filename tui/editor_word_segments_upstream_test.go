package tui

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

func assertEditorUnitCursor(t *testing.T, e *Editor, line, col int) {
	t.Helper()
	if got := e.GetCursor(); got != (EditorCursor{Line: line, Col: col}) {
		t.Fatalf("cursor=%+v, want line=%d col=%d", got, line, col)
	}
}

func TestUpstreamEditorStateAccessors(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:292.
	t.Run("returns cursor position", func(t *testing.T) {
		e := NewEditor()
		assertEditorUnitCursor(t, e, 0, 0)
		e.HandleInput("a")
		e.HandleInput("b")
		e.HandleInput("c")
		assertEditorUnitCursor(t, e, 0, 3)
		e.HandleInput("\x1b[D")
		assertEditorUnitCursor(t, e, 0, 2)
	})
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:307.
	t.Run("returns lines as a defensive copy", func(t *testing.T) {
		e := NewEditor()
		e.SetText("a\nb")
		lines := e.GetLines()
		if !slices.Equal(lines, []string{"a", "b"}) {
			t.Fatalf("lines=%q", lines)
		}
		lines[0] = "mutated"
		if got := e.GetLines(); !slices.Equal(got, []string{"a", "b"}) {
			t.Fatalf("lines after external mutation=%q", got)
		}
	})
}

func TestUpstreamEditorWordNavigation(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:525.
	t.Run("deletes words correctly with Ctrl+W and Alt+Backspace", func(t *testing.T) {
		e := NewEditor()
		for _, tc := range []struct{ text, want string }{
			{"foo bar baz", "foo bar "},
			{"foo bar   ", "foo "},
			{"foo bar...", "foo bar"},
			{"foo.bar", "foo."},
			{"foo:bar", "foo:"},
			{"line one\nline two", "line one\nline "},
			{"line one\n", "line one"},
		} {
			e.SetText(tc.text)
			e.HandleInput("\x17")
			assertEditorText(t, e, tc.want)
		}
		e.SetText("foo 😀😀 bar")
		e.HandleInput("\x17")
		assertEditorText(t, e, "foo 😀😀 ")
		e.HandleInput("\x17")
		assertEditorText(t, e, "foo ")
		e.SetText("foo bar")
		e.HandleInput("\x1b\x7f")
		assertEditorText(t, e, "foo ")
	})
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:575.
	t.Run("navigates words correctly with Ctrl+Left/Right", func(t *testing.T) {
		e := NewEditor()
		e.SetText("foo bar... baz")
		for _, col := range []int{11, 7, 4} {
			e.HandleInput("\x1b[1;5D")
			assertEditorUnitCursor(t, e, 0, col)
		}
		for _, col := range []int{7, 10, 14} {
			e.HandleInput("\x1b[1;5C")
			assertEditorUnitCursor(t, e, 0, col)
		}
		e.SetText("   foo bar")
		e.HandleInput("\x01")
		e.HandleInput("\x1b[1;5C")
		assertEditorUnitCursor(t, e, 0, 6)
		e.SetText("foo.bar baz")
		for _, col := range []int{8, 4, 3} {
			e.HandleInput("\x1b[1;5D")
			assertEditorUnitCursor(t, e, 0, col)
		}
		e.HandleInput("\x01")
		for _, col := range []int{3, 4, 7} {
			e.HandleInput("\x1b[1;5C")
			assertEditorUnitCursor(t, e, 0, col)
		}
	})
	for _, tc := range []struct {
		name, text    string
		back, forward []int
	}{
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:629.
		{"stops at fullwidth Chinese punctuation (issue #4972)", "你好，世界", []int{3, 2, 0}, []int{2, 3, 5}},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:661.
		{"handles mixed CJK and ASCII word movement", "hello你好，world世界", []int{13, 8, 7, 5, 0}, []int{5, 7, 8, 13, 15}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEditor()
			e.SetText(tc.text)
			for _, col := range tc.back {
				e.HandleInput("\x1b[1;5D")
				assertEditorUnitCursor(t, e, 0, col)
			}
			for _, col := range tc.forward {
				e.HandleInput("\x1b[1;5C")
				assertEditorUnitCursor(t, e, 0, col)
			}
		})
	}
}

// .upstream/v0.87.1/packages/tui/test/editor.test.ts:3794-3799.
func upstreamPasteWithMarker(e *Editor) string {
	content := strings.TrimSuffix(strings.Repeat("line\n", 20), "\n")
	e.HandleInput("\x1b[200~" + content + "\x1b[201~")
	return e.Text()
}

var upstreamPasteMarkerPattern = regexp.MustCompile(`\[paste #\d+ \+\d+ lines\]`)

func upstreamMarker(t *testing.T, text string) string {
	t.Helper()
	marker := upstreamPasteMarkerPattern.FindString(text)
	if marker == "" {
		t.Fatalf("missing paste marker in %q", text)
	}
	return marker
}

func TestUpstreamEditorAtomicPasteSegments(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:3806.
	t.Run("creates a paste marker for large pastes", func(t *testing.T) {
		e := NewEditor()
		text := upstreamPasteWithMarker(e)
		if !strings.Contains(upstreamMarker(t, text), "+20 lines") {
			t.Fatalf("marker=%q", text)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:3812.
	t.Run("treats paste marker as single unit for right arrow", func(t *testing.T) {
		e := NewEditor()
		e.HandleInput("A")
		upstreamPasteWithMarker(e)
		e.HandleInput("B")
		e.HandleInput("\x01")
		assertEditorUnitCursor(t, e, 0, 0)
		e.HandleInput("\x1b[C")
		assertEditorUnitCursor(t, e, 0, 1)
		e.HandleInput("\x1b[C")
		marker := upstreamMarker(t, e.Text())
		assertEditorUnitCursor(t, e, 0, 1+len(marker))
		e.HandleInput("\x1b[C")
		assertEditorUnitCursor(t, e, 0, 2+len(marker))
	})
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:3837.
	t.Run("treats paste marker as single unit for left arrow", func(t *testing.T) {
		e := NewEditor()
		e.HandleInput("A")
		upstreamPasteWithMarker(e)
		e.HandleInput("B")
		e.HandleInput("\x1b[D")
		marker := upstreamMarker(t, e.Text())
		assertEditorUnitCursor(t, e, 0, 1+len(marker))
		e.HandleInput("\x1b[D")
		assertEditorUnitCursor(t, e, 0, 1)
		e.HandleInput("\x1b[D")
		assertEditorUnitCursor(t, e, 0, 0)
	})
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:3859.
	t.Run("treats paste marker as single unit for backspace", func(t *testing.T) {
		e := NewEditor()
		e.HandleInput("A")
		upstreamPasteWithMarker(e)
		e.HandleInput("B")
		marker := upstreamMarker(t, e.Text())
		e.HandleInput("\x01")
		e.HandleInput("\x1b[C")
		e.HandleInput("\x1b[C")
		assertEditorUnitCursor(t, e, 0, 1+len(marker))
		e.HandleInput("\x7f")
		assertEditorText(t, e, "AB")
		assertEditorUnitCursor(t, e, 0, 1)
	})
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:3881.
	t.Run("treats paste marker as single unit for forward delete", func(t *testing.T) {
		e := NewEditor()
		e.HandleInput("A")
		upstreamPasteWithMarker(e)
		e.HandleInput("B")
		e.HandleInput("\x01")
		e.HandleInput("\x1b[C")
		e.HandleInput("\x1b[3~")
		assertEditorText(t, e, "AB")
		assertEditorUnitCursor(t, e, 0, 1)
	})
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:3897.
	t.Run("treats paste marker as single unit for word movement", func(t *testing.T) {
		e := NewEditor()
		e.HandleInput("X")
		e.HandleInput(" ")
		upstreamPasteWithMarker(e)
		e.HandleInput(" ")
		e.HandleInput("Y")
		marker := upstreamMarker(t, e.Text())
		e.HandleInput("\x01")
		e.HandleInput("\x1b[1;5C")
		assertEditorUnitCursor(t, e, 0, 1)
		e.HandleInput("\x1b[1;5C")
		assertEditorUnitCursor(t, e, 0, 2+len(marker))
	})
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:4013.
	t.Run("handles multiple paste markers in same line", func(t *testing.T) {
		e := NewEditor()
		upstreamPasteWithMarker(e)
		e.HandleInput(" ")
		upstreamPasteWithMarker(e)
		markers := upstreamPasteMarkerPattern.FindAllString(e.Text(), -1)
		if len(markers) != 2 {
			t.Fatalf("markers=%q, want two pasted blocks", markers)
		}
		e.HandleInput("\x01")
		for _, col := range []int{len(markers[0]), len(markers[0]) + 1, len(markers[0]) + 1 + len(markers[1])} {
			e.HandleInput("\x1b[C")
			assertEditorUnitCursor(t, e, 0, col)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:4042.
	t.Run("does not treat manually typed marker-like text as atomic (no valid paste ID)", func(t *testing.T) {
		e := NewEditor()
		fake := "[paste #99 +5 lines]"
		for _, ch := range fake {
			e.HandleInput(string(ch))
		}
		assertEditorText(t, e, fake)
		e.HandleInput("\x01")
		e.HandleInput("\x1b[C")
		assertEditorUnitCursor(t, e, 0, 1)
	})
}
