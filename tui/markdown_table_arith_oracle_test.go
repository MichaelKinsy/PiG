package tui

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// The column widths of a table are integer arithmetic in markdown.ts renderTable: the natural width of a column (its widest cell), its minimum (its longest
// word, at most 30), the shrink of the minimums when they do not fit (weighted floor shares, then one more column at a time), and the growth of the
// columns when the natural widths do not fit (floor shares of what is left, then one more column at a time up to the natural width). These tests compare
// the rendered table with Pi's at every width from 0 up to the width where the table fits, for tables built to have each (natural, minimum) pair of a
// column, so that every comparison in that arithmetic sits on both sides of its boundary: a mutation of a bound, a sign or an increment changes the
// widths of some table at some width.

// markdownTableCellOfWidth is a cell of exactly natural columns whose words are at most longest columns wide, or "" and false when no such cell exists.
func markdownTableCellOfWidth(natural, longest int) (string, bool) {
	if natural == 0 {
		return "", true
	}
	for words := 1; 2*words-1 <= natural; words++ {
		if natural > words*(longest+1)-1 {
			continue
		}
		extra := natural - (2*words - 1)
		parts := make([]string, words)
		for i := range parts {
			add := min(extra, longest-1)
			extra -= add
			parts[i] = strings.Repeat("x", 1+add)
		}
		return strings.Join(parts, " "), true
	}
	return "", false
}

type markdownTableColumn struct{ longest, natural int }

// markdownTableDoc is a table with a header row and one body row. The column's longest word is in one of the two rows and a cell of its natural width in
// the other, or both in the same row when swap is false and the natural width equals the longest.
func markdownTableDoc(columns []markdownTableColumn, swap []bool) (string, bool) {
	header, body, separator := make([]string, len(columns)), make([]string, len(columns)), make([]string, len(columns))
	for i, c := range columns {
		word := strings.Repeat("w", c.longest)
		cell, ok := markdownTableCellOfWidth(c.natural, c.longest)
		if !ok {
			return "", false
		}
		if c.natural == 0 && c.longest != 1 {
			return "", false
		}
		header[i], body[i] = word, cell
		if swap[i] {
			header[i], body[i] = cell, word
		}
		separator[i] = "---"
	}
	return "| " + strings.Join(header, " | ") + " |\n|" + strings.Join(separator, "|") + "|\n| " + strings.Join(body, " | ") + " |", true
}

// markdownTableProbe is a probe of the doc at every width in 0..fit+3*columns+3, where fit is the width of the table with natural columns.
func markdownTableProbe(doc string, columns int, fit int) markdownRenderProbe {
	widths := make([]int, 0, fit+3*columns+4)
	for w := 0; w <= fit+3*columns+3; w++ {
		widths = append(widths, w)
	}
	return markdownRenderProbe{Text: doc, Widths: widths}
}

func compareMarkdownTablesWithPi(t *testing.T, probes []markdownRenderProbe) {
	t.Helper()
	const batch = 150
	theme := taggedMarkdownTheme()
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	t.Cleanup(ResetCapabilitiesCache)
	failures, renders := 0, 0
	for start := 0; start < len(probes); start += batch {
		chunk := probes[start:min(start+batch, len(probes))]
		expected := piMarkdownRows(t, chunk)
		for i, probe := range chunk {
			md := NewMarkdownWithOptions(probe.Text, probe.PaddingX, probe.PaddingY, &theme, nil, nil)
			for w, width := range probe.Widths {
				renders++
				got := nonNil(md.Render(width))
				if !reflect.DeepEqual(got, nonNil(expected[i][w])) {
					failures++
					if failures <= 10 {
						t.Errorf("table %q width %d:\n got %q\nwant %q", probe.Text, width, got, expected[i][w])
					}
					break
				}
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d tables differ from Pi", failures, len(probes))
	}
	t.Logf("%d tables, %d renders equal Pi's", len(probes), renders)
}

// TestMarkdownTableColumnWidthsMatchPiOneAndTwoColumns builds every table of one column and of two columns from the (longest word, natural width) pairs below,
// in both row orders, and compares the rendering at every width up to where it fits.
func TestMarkdownTableColumnWidthsMatchPiOneAndTwoColumns(t *testing.T) {
	var shapes []markdownTableColumn
	for _, longest := range []int{1, 2, 3, 5, 8, 31, 40} {
		for _, extra := range []int{0, 1, 2, 4, 9, 40} {
			shapes = append(shapes, markdownTableColumn{longest, longest + extra})
		}
	}
	shapes = append(shapes, markdownTableColumn{1, 0})
	var probes []markdownRenderProbe
	add := func(columns []markdownTableColumn, swap []bool) {
		doc, ok := markdownTableDoc(columns, swap)
		if !ok {
			return
		}
		fit := 0
		for _, c := range columns {
			fit += c.natural
		}
		probes = append(probes, markdownTableProbe(doc, len(columns), fit))
	}
	for _, a := range shapes {
		add([]markdownTableColumn{a}, []bool{false})
		add([]markdownTableColumn{a}, []bool{true})
	}
	for _, a := range shapes {
		for _, b := range shapes {
			add([]markdownTableColumn{a, b}, []bool{false, false})
			add([]markdownTableColumn{a, b}, []bool{true, false})
		}
	}
	compareMarkdownTablesWithPi(t, probes)
}

// TestMarkdownTableColumnWidthsMatchPiManyColumns compares random tables of three to eight columns of the same shapes, ragged rows and cells of every kind
// (wide characters, emphasis, code, links, empty cells, words longer than 30 columns), at every width up to where they fit.
func TestMarkdownTableColumnWidthsMatchPiManyColumns(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 9))
	var probes []markdownRenderProbe
	longests := []int{1, 1, 2, 3, 5, 8, 13, 30, 31, 45}
	extras := []int{0, 0, 1, 2, 4, 9, 40, 120}
	for len(probes) < 700 {
		n := 3 + rng.IntN(6)
		columns := make([]markdownTableColumn, n)
		swap := make([]bool, n)
		fit := 0
		for i := range columns {
			l := longests[rng.IntN(len(longests))]
			columns[i] = markdownTableColumn{l, l + extras[rng.IntN(len(extras))]}
			if columns[i].natural == l+1 && l == 1 {
				columns[i].natural = l
			}
			swap[i] = rng.IntN(2) == 0
			fit += columns[i].natural
		}
		doc, ok := markdownTableDoc(columns, swap)
		if !ok || fit > 260 {
			continue
		}
		probes = append(probes, markdownTableProbe(doc, n, fit))
	}
	compareMarkdownTablesWithPi(t, probes)
}

// TestMarkdownTableRandomCellsMatchPi compares tables of random cells at widths 0..(natural width + overhead + 3).
func TestMarkdownTableRandomCellsMatchPi(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 21))
	pieces := []string{"a", "bb", "ccc", "dddd", "e", "漢字", "日本語テキスト", "🙂", "**b**", "*i*", "`code`", "[l](http://x.y/z)", "~~s~~", strings.Repeat("z", 31), strings.Repeat("q", 45), "a\u00a0b", "x\u2003y", "é", "\\|", ""}
	cell := func() string {
		words := rng.IntN(6)
		parts := make([]string, 0, words)
		for range words {
			parts = append(parts, pieces[rng.IntN(len(pieces))])
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	}
	var probes []markdownRenderProbe
	for len(probes) < 600 {
		n := 1 + rng.IntN(5)
		rows := rng.IntN(4)
		var b strings.Builder
		line := func(count int) {
			b.WriteString("|")
			for range count {
				fmt.Fprintf(&b, " %s |", cell())
			}
			b.WriteString("\n")
		}
		line(n)
		b.WriteString("|" + strings.Repeat("---|", n) + "\n")
		for range rows {
			line(max(1, n+rng.IntN(3)-1))
		}
		doc := strings.TrimSuffix(b.String(), "\n")
		probes = append(probes, markdownTableProbe(doc, n, 0))
		last := &probes[len(probes)-1]
		last.Widths = last.Widths[:0]
		for w := 0; w <= 3*n+1+3+10+rng.IntN(120); w++ {
			last.Widths = append(last.Widths, w)
		}
	}
	compareMarkdownTablesWithPi(t, probes)
}
