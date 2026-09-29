package tui

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// When a row has no cell left for the end-of-line cursor (D66), the cursor
// highlights the final grapheme; these lock that path's grapheme slicing.
func TestEditorFullWidthInvalidUTF8DoesNotPanic(t *testing.T) {
	invalid := strings.Repeat("a", 208) + string([]byte{0xe2}) + "x"
	e := NewEditor()
	e.SetText(invalid)

	width := widthx.VisibleWidth(invalid)
	rows := e.buildVisualLines(width, width)
	if len(rows) == 0 {
		t.Fatal("render returned no rows")
	}
	if !strings.Contains(rows[0], "\033[7mx\033[0m") {
		t.Fatalf("cursor did not highlight the final grapheme: %q", rows[0])
	}
}

func TestEditorFullWidthCursorHighlightsFinalGrapheme(t *testing.T) {
	text := strings.Repeat("a", 8) + "e\u0301"
	e := NewEditor()
	e.SetText(text)

	width := widthx.VisibleWidth(text)
	rows := e.buildVisualLines(width, width)
	if !strings.Contains(rows[0], "\033[7me\u0301\033[0m") {
		t.Fatalf("cursor did not highlight the final grapheme: %q", rows[0])
	}
}

func TestEditorGrapheme_LeftRightMovement(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		wantMid int
		wantEnd int
	}{
		{name: "ascii", text: "ab", wantMid: 1, wantEnd: 2},
		{name: "emoji", text: "😊a", wantMid: 2, wantEnd: 3},
		{name: "zwj", text: "👩‍💻a", wantMid: 5, wantEnd: 6},
		{name: "skin-tone", text: "👋🏽a", wantMid: 4, wantEnd: 5},
		{name: "flag", text: "🇺🇸a", wantMid: 4, wantEnd: 5},
		{name: "cjk", text: "界a", wantMid: 1, wantEnd: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEditor()
			e.SetText(tc.text)
			e.cursor = [2]int{0, 0}
			e.HandleInput("\033[C")
			if e.cursor[1] != tc.wantMid {
				t.Fatalf("after right cursor=%d want=%d for %q", e.cursor[1], tc.wantMid, tc.text)
			}
			e.HandleInput("\033[C")
			if e.cursor[1] != tc.wantEnd {
				t.Fatalf("after second right cursor=%d want=%d for %q", e.cursor[1], tc.wantEnd, tc.text)
			}
			e.HandleInput("\033[D")
			if e.cursor[1] != tc.wantMid {
				t.Fatalf("after left cursor=%d want=%d for %q", e.cursor[1], tc.wantMid, tc.text)
			}
		})
	}
}

func TestEditorGrapheme_BackspaceAndDelete(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "emoji", text: "A😊B"},
		{name: "zwj", text: "A👩‍💻B"},
		{name: "skin-tone", text: "A👋🏽B"},
		{name: "flag", text: "A🇺🇸B"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/backspace", func(t *testing.T) {
			e := NewEditor()
			e.SetText(tc.text)
			e.cursor = [2]int{0, len(utf16.Encode([]rune(tc.text))) - 1}
			e.HandleInput("\x7f")
			if got := e.Text(); got != "AB" {
				t.Fatalf("backspace got=%q want=%q", got, "AB")
			}
		})
		t.Run(tc.name+"/delete", func(t *testing.T) {
			e := NewEditor()
			e.SetText(tc.text)
			e.cursor = [2]int{0, len("A")}
			e.HandleInput("\x1b[3~")
			if got := e.Text(); got != "AB" {
				t.Fatalf("delete got=%q want=%q", got, "AB")
			}
		})
	}
}

func TestEditorMouseGraphemeBoundaries(t *testing.T) {
	for _, text := range []string{"👩‍💻", "🇺🇸"} {
		for _, target := range []int{1, 2} {
			e := NewEditor()
			e.SetText(text)
			e.Render(80)
			e.HandleMouse(TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft, X: target, Y: 1, Width: 80})
			want := 0
			if target == 2 {
				want = len(utf16.Encode([]rune(text)))
			}
			if got := e.GetCursor().Col; got != want {
				t.Fatalf("mouse(%q,%d)=%d want=%d", text, target, got, want)
			}
		}
	}
}

func TestWordWrapLineGraphemeBoundary(t *testing.T) {
	chunks := wordWrapLine("👩‍💻a", 2, nil)
	if len(chunks) != 2 {
		t.Fatalf("len(chunks)=%d want 2 (%v)", len(chunks), chunks)
	}
	if chunks[0].text != "👩‍💻" || chunks[0].startIndex != 0 {
		t.Fatalf("first chunk=%+v", chunks[0])
	}
	if chunks[1].text != "a" || chunks[1].startIndex != 5 {
		t.Fatalf("second chunk=%+v want UTF-16 start=5", chunks[1])
	}
}

func TestEditorWordMovementPunctuationAndCrossLine(t *testing.T) {
	e := NewEditor()
	e.SetText("foo, bar.\nbaz")
	e.cursor = [2]int{0, len("foo, bar.")}
	e.HandleInput("\x1bb")
	if e.cursor != [2]int{0, len("foo, bar")} {
		t.Fatalf("alt+b from trailing punctuation cursor=%v want [0 %d]", e.cursor, len("foo, bar"))
	}
	e.HandleInput("\x1bb")
	if e.cursor != [2]int{0, len("foo, ")} {
		t.Fatalf("second alt+b cursor=%v want [0 %d]", e.cursor, len("foo, "))
	}
	e.cursor = [2]int{0, len("foo, bar.")}
	e.HandleInput("\x1b[1;3C")
	if e.cursor != [2]int{1, 0} {
		t.Fatalf("alt+right at end of line should move to next line start, got %v", e.cursor)
	}
}
