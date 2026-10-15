package tui

import "testing"

// packages/tui/src/colors.ts:330-370 styleTextWithAnsi: the given foreground and background sequences open first (an empty one is unset), then bold, dim, italic,
// underline, inverse and strikethrough; the closing sequences come in reverse order of the opening ones (bold and dim share one 22 reset, emitted once).
func TestStyleTextWithAnsiOpensAndClosesInReverseOrder(t *testing.T) {
	const fg, bg = "\x1b[31m", "\x1b[41m"
	all := TextAttributes{Bold: true, Dim: true, Italic: true, Underline: true, Inverse: true, Strikethrough: true}
	for _, tc := range []struct {
		name       string
		fg, bg     string
		attributes TextAttributes
		want       string
	}{
		{"nothing", "", "", TextAttributes{}, "x"},
		{"foreground only", fg, "", TextAttributes{}, fg + "x\x1b[39m"},
		{"background only", "", bg, TextAttributes{}, bg + "x\x1b[49m"},
		{"both colors", fg, bg, TextAttributes{}, fg + bg + "x\x1b[49m\x1b[39m"},
		{"bold", "", "", TextAttributes{Bold: true}, "\x1b[1mx\x1b[22m"},
		{"dim", "", "", TextAttributes{Dim: true}, "\x1b[2mx\x1b[22m"},
		{"bold and dim share one reset", "", "", TextAttributes{Bold: true, Dim: true}, "\x1b[1m\x1b[2mx\x1b[22m"},
		{"italic", "", "", TextAttributes{Italic: true}, "\x1b[3mx\x1b[23m"},
		{"underline", "", "", TextAttributes{Underline: true}, "\x1b[4mx\x1b[24m"},
		{"inverse", "", "", TextAttributes{Inverse: true}, "\x1b[7mx\x1b[27m"},
		{"strikethrough", "", "", TextAttributes{Strikethrough: true}, "\x1b[9mx\x1b[29m"},
		{"everything", fg, bg, all, fg + bg + "\x1b[1m\x1b[2m\x1b[3m\x1b[4m\x1b[7m\x1b[9m" + "x" + "\x1b[29m\x1b[27m\x1b[24m\x1b[23m\x1b[22m\x1b[49m\x1b[39m"},
	} {
		if got := StyleTextWithAnsi("x", tc.fg, tc.bg, tc.attributes); got != tc.want {
			t.Errorf("%s: StyleTextWithAnsi = %q, want %q", tc.name, got, tc.want)
		}
	}
}
