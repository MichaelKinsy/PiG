package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestEditorFullWidthInvalidUTF8DoesNotPanic(t *testing.T) {
	invalid := strings.Repeat("a", 208) + string([]byte{0xe2}) + "x"
	e := NewEditor()
	e.SetText(invalid)

	rows := e.buildVisualLines(widthx.VisibleWidth(invalid))
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

	rows := e.buildVisualLines(widthx.VisibleWidth(text))
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
		{name: "emoji", text: "😊a", wantMid: len("😊"), wantEnd: len("😊a")},
		{name: "zwj", text: "👩‍💻a", wantMid: len("👩‍💻"), wantEnd: len("👩‍💻a")},
		{name: "skin-tone", text: "👋🏽a", wantMid: len("👋🏽"), wantEnd: len("👋🏽a")},
		{name: "flag", text: "🇺🇸a", wantMid: len("🇺🇸"), wantEnd: len("🇺🇸a")},
		{name: "cjk", text: "界a", wantMid: len("界"), wantEnd: len("界a")},
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
			e.cursor = [2]int{0, len(tc.text) - len("B")}
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

func TestByteOffsetForColumnGrapheme(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		target int
		want   int
	}{
		{name: "zwj-width-2-col-1", text: "👩‍💻", target: 1, want: 0},
		{name: "zwj-width-2-col-2", text: "👩‍💻", target: 2, want: len("👩‍💻")},
		{name: "flag-width-2-col-1", text: "🇺🇸", target: 1, want: 0},
		{name: "flag-width-2-col-2", text: "🇺🇸", target: 2, want: len("🇺🇸")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := byteOffsetForColumnGrapheme(tc.text, tc.target); got != tc.want {
				t.Fatalf("byteOffsetForColumnGrapheme(%q, %d) = %d want %d", tc.text, tc.target, got, tc.want)
			}
		})
	}
}

func TestChunkByWidthGrapheme(t *testing.T) {
	chunks, starts := chunkByWidth("👩‍💻a", 2)
	if len(chunks) != 2 {
		t.Fatalf("len(chunks)=%d want 2 (%v)", len(chunks), chunks)
	}
	if chunks[0] != "👩‍💻" || starts[0] != 0 {
		t.Fatalf("first chunk=%q start=%d", chunks[0], starts[0])
	}
	if chunks[1] != "a" || starts[1] != len("👩‍💻") {
		t.Fatalf("second chunk=%q start=%d want start=%d", chunks[1], starts[1], len("👩‍💻"))
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
