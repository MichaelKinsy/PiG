package tui

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func assertEditorUnits(t *testing.T, e *Editor, want []uint16, col int) {
	t.Helper()
	if got := jsstring.ToUTF16(e.Text()); !slices.Equal(got, want) {
		t.Fatalf("text units=%04x want=%04x", got, want)
	}
	if got := e.GetCursor(); got.Col != col {
		t.Fatalf("cursor=%+v want column=%d", got, col)
	}
}

// Pi editor.ts insertCharacter slices and advances by UTF-16 units. Sequential stdin surrogate events rejoin before grapheme editing.
func TestEditorSequentialSurrogateEvents(t *testing.T) {
	e := NewEditor()
	var changes [][]uint16
	e.OnChange = func(text string) { changes = append(changes, jsstring.ToUTF16(text)) }
	e.HandleInput(jsstring.FromUTF16([]uint16{0xd83d}))
	assertEditorUnits(t, e, []uint16{0xd83d}, 1)
	e.HandleInput(jsstring.FromUTF16([]uint16{0xde00}))
	assertEditorUnits(t, e, []uint16{0xd83d, 0xde00}, 2)
	if e.Text() != "😀" {
		t.Fatalf("joined text is not canonical UTF-8: %x", e.Text())
	}
	if len(changes) != 2 || !slices.Equal(changes[0], []uint16{0xd83d}) || !slices.Equal(changes[1], []uint16{0xd83d, 0xde00}) {
		t.Fatalf("onChange=%04x", changes)
	}
	e.HandleInput("\x7f")
	assertEditorUnits(t, e, []uint16{}, 0)
}

// Pi editor.ts jumpToChar uses String.indexOf/lastIndexOf, so a raw surrogate search can put the cursor inside a pair.
func TestEditorUTF16HalfCursorEditing(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		want      []uint16
		col       int
	}{
		{"insert", "X", []uint16{'A', 0xd83d, 'X', 0xde00, 'B'}, 3},
		{"backspace", "\x7f", []uint16{'A', 0xde00, 'B'}, 1},
		{"forward delete", "\x1b[3~", []uint16{'A', 0xd83d, 'B'}, 2},
		{"newline", "\n", []uint16{'A', 0xd83d, '\n', 0xde00, 'B'}, 0},
		{"kill backward", "\x15", []uint16{0xde00, 'B'}, 0},
		{"kill forward", "\x0b", []uint16{'A', 0xd83d}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEditor()
			e.SetText("A😀B")
			e.HandleInput("\x01")
			e.HandleInput("\x1d")
			e.HandleInput(jsstring.FromUTF16([]uint16{0xde00}))
			assertEditorUnits(t, e, []uint16{'A', 0xd83d, 0xde00, 'B'}, 2)
			e.HandleInput(tc.key)
			assertEditorUnits(t, e, tc.want, tc.col)
		})
	}
}

func TestEditorVerticalMovementUsesUTF16Columns(t *testing.T) {
	for _, tc := range []struct {
		text string
		want EditorCursor
	}{
		{"äx\na", EditorCursor{0, 1}},
		{"😀x\na", EditorCursor{0, 0}},
		{"A😀B\nabc", EditorCursor{0, 3}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			e := NewEditor()
			e.SetText(tc.text)
			e.HandleInput("\x1b[A")
			if got := e.GetCursor(); got != tc.want {
				t.Fatalf("cursor=%+v want=%+v", got, tc.want)
			}
			e.HandleInput("X")
			if got := e.GetCursor(); got.Col != tc.want.Col+1 {
				t.Fatalf("cursor after insert=%+v", got)
			}
		})
	}
}

func TestEditorPastePreservesSurrogateUnits(t *testing.T) {
	e := NewEditor()
	e.HandleInput("\x1b[200~" + jsstring.FromUTF16([]uint16{0xd83d, 'X', 0xde00}) + "\x1b[201~")
	assertEditorUnits(t, e, []uint16{0xd83d, 'X', 0xde00}, 3)
}

func TestEditorHalfCursorProgrammaticInsertion(t *testing.T) {
	e := NewEditor()
	e.SetText("A😀B")
	e.HandleInput("\x01")
	e.HandleInput("\x1d")
	e.HandleInput(jsstring.FromUTF16([]uint16{0xde00}))
	e.InsertTextAtCursor("X\nY")
	assertEditorUnits(t, e, []uint16{'A', 0xd83d, 'X', '\n', 'Y', 0xde00, 'B'}, 1)
	e.HandleInput(kittyUndo)
	assertEditorUnits(t, e, []uint16{'A', 0xd83d, 0xde00, 'B'}, 2)
}
