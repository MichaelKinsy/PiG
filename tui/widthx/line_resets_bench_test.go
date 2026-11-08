package widthx

import (
	"strings"
	"testing"
)

// BenchmarkApplyLineReset is the per-line cost of the line resets on the main-screen render path: every line but the cursor's has no marker, one has
// a stray marker. The cases are an 80-column line without styling (plain), the same width with SGR codes (styled; the "plain" case before the
// cases were split), a styled line with one marker (marker) and with two (markers2), a line of wide glyphs (wide), a line with a tab and one with a
// Thai AM vowel (both rewritten by NormalizeTerminalOutput), and the same line 1000 columns wide, without and with a marker (col1000,
// col1000_marker).
func BenchmarkApplyLineReset(b *testing.B) {
	styled := "\x1b[31m" + strings.Repeat("hello world ", 6) + "\x1b[0m tail"
	wide := "\x1b[1m" + strings.Repeat("日本語のテキスト", 4) + "\x1b[0m🙂 " + strings.Repeat("字", 15)
	wideThai := "\x1b[1m" + strings.Repeat("ข้อความ ", 8) + "\x1b[0m"
	col1000 := strings.Repeat("\x1b[32mhello wor\x1b[0mld ", 1000/12) + "tail"
	cases := []struct{ name, line string }{
		{"plain", strings.Repeat("hello world ", 6) + " tail"},
		{"styled", styled},
		{"marker", styled + CursorMarker},
		{"markers2", "\x1b[31m" + CursorMarker + strings.Repeat("hello world ", 6) + "\x1b[0m tail" + CursorMarker},
		{"wide", wide},
		{"wide_marker", CursorMarker + wide},
		{"tab", styled + "\tx"},
		{"thai", wideThai + "นำ"},
		{"col1000", col1000},
		{"col1000_marker", col1000 + CursorMarker},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = ApplyLineReset(c.line)
			}
		})
	}
}
