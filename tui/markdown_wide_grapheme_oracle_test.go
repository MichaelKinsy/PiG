package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// A grapheme wider than the width is a row of its own, wider than the width, in Pi's Markdown: wrapTextWithAnsi's breakLongWord (packages/tui/src/utils.ts) starts a
// new row when the grapheme does not fit, then appends it whatever its width. PiG renders the same rows (this test compares them at widths 1 to 80, in paragraphs,
// headings, lists, quotes, code and tables), and FuzzMarkdownRenderStaysWithinWidth allows exactly this row (corpus entry 7cc982767969d7bd: 툚 at width 1). The second
// half pins the premise in Pi itself, so the exception goes when Pi stops emitting the row.
func TestMarkdownWideGraphemeAtOneColumnMatchesPi(t *testing.T) {
	docs := []string{"툚", "日", "😀", "👨‍👩‍👧", "a툚b", "# 툚", "- 툚", "1. 😀 a", "> 툚", "`툚`", "```\n툚\n```", "| 툚 |\n|---|\n| 툚 |"}
	compareMarkdownWithPi(t, docs)
	probes := make([]markdownRenderProbe, len(docs))
	for i, doc := range docs[:4] {
		probes[i] = markdownRenderProbe{Text: doc, Widths: []int{1}}
	}
	for i, rows := range piMarkdownRows(t, probes[:4]) {
		wide := 0
		for _, row := range rows[0] {
			if widthx.VisibleWidth(row) > 1 {
				wide++
				if !markdownRowIsOneOverWideGrapheme(row, 1) {
					t.Errorf("Pi's row %q of %q at width 1 is wider than the width but is not one grapheme", row, docs[i])
				}
			}
		}
		if wide != 1 {
			t.Errorf("Pi renders %q at width 1 with %d rows wider than the width (%q), want the one row of the grapheme", docs[i], wide, rows[0])
		}
	}
}
