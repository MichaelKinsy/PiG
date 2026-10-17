package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Any sequence of keys, pastes and text typed into the Editor leaves it rendering without a panic, within the width, with valid UTF-8 text.
func FuzzEditorInputKeepsRenderValid(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, "hello wörld 日本語 😀", 30)
	f.Add([]byte{20, 21, 22, 23, 0, 0, 0}, "a\nb\n\nc", 10)
	keys := []string{"\r", "\n", "\x7f", "\b", "\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", "\x1b[H", "\x1b[F", "\x1b[3~", "\x1b[5~", "\x1b[6~", "\x01", "\x05", "\x0b", "\x15", "\x17", "\x19", "\x1a",
		"\x1b[1;5C", "\x1b[1;5D", "\x1b[1;3C", "\x1b[1;3D", "\x1bb", "\x1bf", "\x1bd", "\x1b[200~pasted\ntext\x1b[201~", "\x1b[200~\x1b[201~", "\t", " ", "\x1b[13;2u", "\x1b[127;5u", "\x1b\r", "\x1b[Z", "\x1b[27u"}
	f.Fuzz(func(t *testing.T, script []byte, text string, width int) {
		if width < 0 {
			width = -width
		}
		width = 3 + width%100
		e := NewEditor()
		for i, b := range script {
			if i%7 == 6 {
				e.HandleInput(text)
				continue
			}
			e.HandleInput(keys[int(b)%len(keys)])
		}
		for _, line := range e.Render(width) {
			if w := widthx.VisibleWidth(line); w > width {
				t.Fatalf("editor line is %d columns wide, width is %d: %q", w, width, line)
			}
		}
		if got := e.Text(); !utf8.ValidString(got) && utf8.ValidString(text) && !strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("editor text is not valid UTF-8: %q", got)
		}
	})
}
