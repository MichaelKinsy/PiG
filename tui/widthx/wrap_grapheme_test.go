package widthx

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Pinned utils.ts breakLongWord measures each grapheme with visibleWidth, not
// code-point width. Direct source probes return [4, 2] cells for the warning
// and flag triples at width 4, including these exact SGR/OSC 8 continuations.
func TestWrapTextWithAnsiGraphemeCellWidths(t *testing.T) {
	for _, glyph := range []struct {
		name, text string
		cells      int
	}{
		{"warning", "⚠️", 2},
		{"flag", "🇺🇸", 2},
		{"cjk", "界", 2},
		{"zwj", "👩‍💻", 2},
		{"combining", "é", 1},
		{"ascii", "a", 1},
	} {
		for _, style := range []struct{ name, open, lineEnd, close string }{
			{"plain", "", "", ""},
			{"sgr", "\x1b[4;31;44m", "\x1b[24m", "\x1b[0m"},
			{"bel-link", "\x1b[4;31;44m\x1b]8;id=wrap;https://example.test\x07", "\x1b[24m\x1b]8;;\x07", "\x1b]8;;\x07\x1b[0m"},
			{"st-link", "\x1b[4;31;44m\x1b]8;id=wrap;https://example.test\x1b\\", "\x1b[24m\x1b]8;;\x1b\\", "\x1b]8;;\x1b\\\x1b[0m"},
		} {
			for _, perRow := range []int{1, 2, 3} {
				t.Run(fmt.Sprintf("%s/%s/%d", glyph.name, style.name, perRow), func(t *testing.T) {
					text := style.open + strings.Repeat(glyph.text, 3) + style.close
					if got := VisibleWidth(text); got != 3*glyph.cells {
						t.Fatalf("source width = %d, want %d", got, 3*glyph.cells)
					}
					var want []string
					var widths []int
					for remaining := 3; remaining > 0; remaining -= perRow {
						n := min(remaining, perRow)
						end := style.lineEnd
						if remaining <= perRow {
							end = style.close
						}
						want = append(want, style.open+strings.Repeat(glyph.text, n)+end)
						widths = append(widths, n*glyph.cells)
					}
					got := WrapTextWithAnsi(text, perRow*glyph.cells)
					if !slices.Equal(got, want) {
						t.Fatalf("wrapped bytes = %q, want %q", got, want)
					}
					for i, row := range got {
						if w := VisibleWidth(row); w != widths[i] {
							t.Errorf("row %d width = %d, want %d", i, w, widths[i])
						}
					}
					if plain := StripAnsi(strings.Join(got, "")); plain != strings.Repeat(glyph.text, 3) {
						t.Fatalf("text changed: %q", plain)
					}
				})
			}
		}
	}
}
