package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// inverseCells counts the cells a terminal paints in reverse video for line,
// tracking SGR 7 (on), 27 and 0 (off).
func inverseCells(line string) int {
	inverse, cells := false, 0
	for i := 0; i < len(line); {
		if n := widthx.ExtractAnsi(line, i); n > 0 {
			code := line[i : i+n]
			if strings.HasPrefix(code, "\x1b[") && strings.HasSuffix(code, "m") {
				for param := range strings.SplitSeq(code[2:len(code)-1], ";") {
					switch param {
					case "7":
						inverse = true
					case "", "0", "27":
						inverse = false
					}
				}
			}
			i += n
			continue
		}
		end := i
		for end < len(line) && widthx.ExtractAnsi(line, end) == 0 {
			end++
		}
		if inverse {
			cells += widthx.VisibleWidth(line[i:end])
		}
		i = end
	}
	return cells
}

func editorCursorRow(t *testing.T, lines []string) string {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line, "\x1b[7m") {
			return line
		}
	}
	t.Fatalf("no cursor row in %q", lines)
	return ""
}

// Pi's Editor.render pads every content row to the content width, so the
// cursor's closing reset is followed by cells and survives Pi's
// CompositeTuiLine, which drops SGR codes after the last "before" cell. An
// overlay covering the editor row must leave exactly one inverse cell.
func TestOverlayOverEditorRowKeepsOneCursorCell(t *testing.T) {
	const width = 40
	for _, tc := range []struct {
		name, text string
		paddingX   int
		focused    bool
	}{
		{name: "empty unfocused"},
		{name: "empty focused", focused: true},
		{name: "cursor at end", text: "hi"},
		{name: "padded", text: "hi", paddingX: 2},
		{name: "cursor in right padding", text: strings.Repeat("x", 36), paddingX: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			editor := NewEditor()
			editor.SetPaddingX(tc.paddingX)
			editor.Focused = tc.focused
			editor.SetText(tc.text)
			row := editorCursorRow(t, editor.Render(width))
			if got := widthx.VisibleWidth(row); got != width {
				t.Fatalf("editor row width = %d, want %d (Pi pads rows to the content width): %q", got, width, row)
			}
			composed := CompositeTuiLine(row, "│box│", 12, 5, width)
			if got := inverseCells(composed); got != 1 {
				t.Fatalf("inverse cells = %d, want 1: %q", got, composed)
			}
		})
	}
}

// Pi's Editor.render appends the cursor as a highlighted space after a line
// that exactly fills the layout width: without padding the reserved cursor
// column holds it, and with padding it takes one right-padding cell. Rows are
// Pi 0.87.1's Editor.render output under node.
func TestEditorCursorAfterLineFillingLayoutWidth(t *testing.T) {
	for _, tc := range []struct {
		name        string
		text        string
		width, padX int
		want        string
	}{
		{name: "reserved column", text: strings.Repeat("x", 39), width: 40, want: strings.Repeat("x", 39) + "\x1b[7m \x1b[0m"},
		{name: "right padding", text: strings.Repeat("x", 36), width: 40, padX: 2, want: "  " + strings.Repeat("x", 36) + "\x1b[7m \x1b[0m "},
		{name: "one-column padding", text: "g", width: 3, padX: 1, want: " g\x1b[7m \x1b[0m"},
		{name: "width 2", text: "g", width: 2, want: "g\x1b[7m \x1b[0m"},
		{name: "combining mark", text: strings.Repeat("a", 8) + "é", width: 10, want: strings.Repeat("a", 8) + "é\x1b[7m \x1b[0m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			editor := NewEditor()
			editor.SetPaddingX(tc.padX)
			editor.SetText(tc.text)
			if got := editor.Render(tc.width)[1]; got != tc.want {
				t.Fatalf("cursor row = %q, want %q", got, tc.want)
			}
		})
	}
}

// At width 1 without padding Pi 0.87.1's Editor.render returns "g\x1b[7m \x1b[0m",
// two cells wide, which its main screen treats as fatal. PiG highlights the
// final grapheme instead so the row fits (D66); an empty editor matches Pi.
func TestEditorCursorAtWidthOneStaysInBounds(t *testing.T) {
	for text, want := range map[string]string{"g": "\x1b[7mg\x1b[0m", "": "\x1b[7m \x1b[0m"} {
		editor := NewEditor()
		editor.SetText(text)
		if got := editor.Render(1)[1]; got != want {
			t.Fatalf("width-1 row for %q = %q, want %q", text, got, want)
		}
	}
}
