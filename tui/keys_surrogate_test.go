package tui

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi keys.ts uses String.fromCodePoint, which preserves surrogate code points rather than replacing them as Go string(rune) does.
func TestPrintableKeyPreservesSurrogateCodepoints(t *testing.T) {
	for _, tc := range []struct {
		data string
		unit uint16
	}{
		{"\x1b[55357u", 0xd83d}, {"\x1b[56832u", 0xde00}, {"\x1b[27;1;55357~", 0xd83d}, {"\x1b[27;1;56832~", 0xde00},
	} {
		t.Run(tc.data, func(t *testing.T) {
			text, ok := DecodePrintableKey(tc.data)
			if !ok || !slices.Equal(jsstring.ToUTF16(text), []uint16{tc.unit}) {
				t.Fatalf("decoded=%04x ok=%v want=%04x", jsstring.ToUTF16(text), ok, tc.unit)
			}
		})
	}
	e := NewEditor()
	e.HandleInput("\x1b[55357u")
	e.HandleInput("\x1b[56832u")
	if e.Text() != "😀" || e.GetCursor().Col != 2 {
		t.Fatalf("text=%q cursor=%+v", e.Text(), e.GetCursor())
	}
}
