package tui

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// markdownRandomTables are tables of one to seven columns drawn from a seeded generator: short, empty, long, unbroken (below and above the 30-column word
// cap), wide, emphasised and escaped cells; rows with missing and extra cells; zero to five body rows. Their natural widths straddle every threshold of
// markdown.ts renderTable's column arithmetic (the raw-markdown fallback, the per-word minimums, the weighted shrink and the grow loop).
func markdownRandomTables(seed uint64, count int) []string {
	rng := rand.New(rand.NewPCG(seed, seed^0x2545f4914f6cdd1d))
	words := []string{"a", "ab", "abc", "word", "longer", "sixteen-chars-xyz", strings.Repeat("x", 29), strings.Repeat("y", 30), strings.Repeat("z", 31), strings.Repeat("w", 45), "日本語", "🙂", "**b**", "`c`", "[l](http://u.v)", "a\\|b", "e\u0301"}
	cell := func() string {
		if rng.IntN(8) == 0 {
			return ""
		}
		out := words[rng.IntN(len(words))]
		for rng.IntN(3) == 0 {
			out += " " + words[rng.IntN(len(words))]
		}
		return out
	}
	docs := make([]string, 0, count)
	for range count {
		cols := 1 + rng.IntN(7)
		line := func(n int) string {
			cells := make([]string, n)
			for i := range cells {
				cells[i] = cell()
			}
			return "| " + strings.Join(cells, " | ") + " |"
		}
		lines := []string{line(cols), "|" + strings.Repeat(" --- |", cols)}
		for range rng.IntN(6) {
			n := cols
			switch rng.IntN(6) {
			case 0:
				n = rng.IntN(cols + 1)
			case 1:
				n = cols + 1 + rng.IntN(2)
			}
			lines = append(lines, line(n))
		}
		docs = append(docs, strings.Join(lines, "\n"))
	}
	return docs
}

// TestMarkdownRandomTablesMatchPi renders markdownRandomTables at every width from 0 to 48 and at 60, 80 and 100, with the default constructor and with a text color, and requires the rows to equal pi-tui's.
func TestMarkdownRandomTablesMatchPi(t *testing.T) {
	widths := make([]int, 0, 52)
	for w := 0; w <= 48; w++ {
		widths = append(widths, w)
	}
	widths = append(widths, 60, 80, 100)
	var probes []markdownContractProbe
	for i, doc := range markdownRandomTables(20260301, 1500) {
		probe := markdownContractProbe{Text: doc, PaddingX: i % 2, PaddingY: 0, Theme: "tagged", Ops: markdownContractRenderOps(widths...)}
		if i%3 == 0 {
			probe.Style = &markdownContractStyle{Color: true}
		}
		probes = append(probes, probe)
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// TestMarkdownTableColumnsOfNoWidthMatchPi renders tables with a column whose cells are all empty or all zero-width (a combining mark or a zero-width space is
// a word of width 0), beside wide columns, at every width from 0 to 70. markdown.ts renderTable gives such a column the minimum width 1 and a natural
// width 0, so the grow potential of its column is clamped from below at zero.
func TestMarkdownTableColumnsOfNoWidthMatchPi(t *testing.T) {
	long := "long cell text that has to wrap when the table is narrow"
	docs := []string{
		"| | b |\n|---|---|\n| | " + long + " |",
		"| a | | c |\n|---|---|---|\n| " + long + " | | " + long + " |\n| x | | y |",
		"| | |\n|---|---|\n| | |\n| | |",
		"| \u0301 | b |\n|---|---|\n| \u0301 | " + long + " |",
		"| \u200b | " + long + " |\n|---|---|\n| \u200b | c |",
		"| \u0301 \u0301 | " + long + " |\n|---|---|\n| \u200b\u200b | " + long + " |",
		"| \u0301 |\n|---|\n| \u0301 |",
		"| \u200b |\n|---|",
		"| | " + strings.Repeat("w", 40) + " | | " + strings.Repeat("v", 35) + " |\n|---|---|---|---|\n| | a | | b |",
		"| | a b c d e f g h i j |\n|---|---|\n| | k l m n o p q r s t |",
	}
	widths := make([]int, 0, 71)
	for w := 0; w <= 70; w++ {
		widths = append(widths, w)
	}
	var probes []markdownContractProbe
	for _, doc := range docs {
		for _, padX := range []int{0, 1} {
			probes = append(probes, markdownContractProbe{Text: doc, PaddingX: padX, Theme: "tagged", Ops: markdownContractRenderOps(widths...)})
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
