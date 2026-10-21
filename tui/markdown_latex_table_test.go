package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var (
	markdownLatexTable         = []string{"| A | B |", "|---|---|", "| 1 | 2 |"}
	markdownLatexRenderedTable = []string{"┌───┬───┐", "│ A │ B │", "├───┼───┤", "│ 1 │ 2 │", "└───┴───┘"}
)

func markdownLatexTableJoin(parts ...[]string) []string {
	return slices.Concat(parts...)
}

// TestMarkdownUnclosedDisplayMathBeforeTables ports packages/tui/test/markdown.test.ts "unclosed display math before
// tables (#11159)" (2ed3118b), all eight cases verbatim: Markdown at width 80, ANSI stripped, trimEnd per line.
func TestMarkdownUnclosedDisplayMathBeforeTables(t *testing.T) {
	render := func(source string) []string {
		return markdownPlainLines(NewMarkdown(source).Render(80))
	}
	table, renderedTable := markdownLatexTable, markdownLatexRenderedTable
	j := markdownLatexTableJoin
	cases := []struct {
		name   string
		source []string
		want   []string
	}{
		{"renders a table after an orphan $$ opener",
			j([]string{"Intro", "", "$$", ""}, table, []string{"", "After"}),
			j([]string{"Intro", "", "$$", ""}, renderedTable, []string{"", "After"})},
		{"renders a table after an orphan $$ opener with math-like content",
			j([]string{"$$", "x = 1", ""}, table),
			j([]string{"$$", "x = 1", ""}, renderedTable)},
		{"does not pair an unclosed $$ with a later $$ across a table",
			j([]string{"Intro", "", "$$", "x = 1", ""}, table, []string{"", "$$", "y^2", "$$"}),
			j([]string{"Intro", "", "$$", "x = 1", ""}, renderedTable, []string{"", "y²"})},
		{"renders a single-column table with a pipe-free aligned delimiter after an orphan $$",
			[]string{"$$", "", "| A |", ":---:", "| 1 |"},
			[]string{"$$", "", "┌───┐", "│ A │", "├───┤", "│ 1 │", "└───┘"}},
		{"renders a table after an orphan \\[ opener",
			j([]string{"\\\\[", "x^2", ""}, table),
			j([]string{"", "\\[", "x^2", ""}, renderedTable)},
		{"does not pair an unclosed \\[ with a later \\[ across a table",
			j([]string{"\\\\[", "x = 1", ""}, table, []string{"", "\\\\[", "y^2", "\\\\]"}),
			j([]string{"", "\\[", "x = 1", ""}, renderedTable, []string{"", "\\", "", "\\[", "y^2", "\\\\]"})},
		{"renders a table with a pipe-free header after an orphan $$",
			[]string{"$$", "x^2", "", "Value", "| --- |", "1"},
			[]string{"$$", "x^2", "", "┌───────┐", "│ Value │", "├───────┤", "│ 1     │", "└───────┘"}},
		{"still renders closed multi-line display math followed by a table",
			j([]string{"$$", "a", "+ b", "$$", ""}, table),
			j([]string{"a + b", ""}, renderedTable)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := render(strings.Join(tc.source, "\n")); !slices.Equal(got, tc.want) {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestMarkdownContainsTableStart pins the helper's three rules (markdown.ts containsTableStart, 2ed3118b): a pipe in
// the delimiter row or the header row, and a delimiter row that matches TABLE_DELIMITER_ROW_REGEX.
func TestMarkdownContainsTableStart(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   bool
	}{
		{"| A | B |\n|---|---|", true},
		{"| A |\n| --- |", true},
		{"Value\n| --- |", true},
		{"| A |\n:---:", true},
		{"Value\n---", false}, // thematic break or setext underline: no pipe in either row
		{"A\n---", false},
		{"| A |\n|---|x", false},
		{"|---|---|", false},     // a delimiter needs a header row above it
		{"a | b\n    ---", true}, // the regex's [ \t]* after the optional pipe takes the extra indent, as in Pi
		{"", false},
	} {
		if got := containsTableStart(tc.source); got != tc.want {
			t.Errorf("containsTableStart(%q) = %v, want %v", tc.source, got, tc.want)
		}
	}
}

// TestMarkdownUnclosedDisplayMathBeforeTablesMatchesPi renders the eight Pi cases and their neighbours (the `\[`
// pending bracket now needs a non-empty body, a table inside a closed block, the guards behind a list or quote) in the
// live Pi so Pi decides.
func TestMarkdownUnclosedDisplayMathBeforeTablesMatchesPi(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "extensions", "sdk-ts", "node_modules")); err != nil {
		t.Skip("the Pi oracle is not installed (make parity-deps)")
	}
	table, j := markdownLatexTable, markdownLatexTableJoin
	join := func(lines []string) string { return strings.Join(lines, "\n") }
	docs := []string{
		join(j([]string{"Intro", "", "$$", ""}, table, []string{"", "After"})),
		join(j([]string{"$$", "x = 1", ""}, table)),
		join(j([]string{"Intro", "", "$$", "x = 1", ""}, table, []string{"", "$$", "y^2", "$$"})),
		join([]string{"$$", "", "| A |", ":---:", "| 1 |"}),
		join(j([]string{"\\\\[", "x^2", ""}, table)),
		join(j([]string{"\\\\[", "x = 1", ""}, table, []string{"", "\\\\[", "y^2", "\\\\]"})),
		join([]string{"$$", "x^2", "", "Value", "| --- |", "1"}),
		join(j([]string{"$$", "a", "+ b", "$$", ""}, table)),
		// Neighbours: one-column and aligned delimiters, a thematic break and a setext underline are not tables.
		join(j([]string{"\\[", "x^2", ""}, table)),
		join(j([]string{"\\[", "x = 1", ""}, table, []string{"", "\\[", "y^2", "\\]"})),
		"\\[", "\\[\n", "\\[ ", "\\[\n\nx", "\\[x", "$$\nx", "$$\nx^2\n---\n", "$$\nValue\n---\n$$", "\\[\nValue\n---\n\\]",
		"$$\nx^2\n\nA\n---\n\n1", "$$\nx^2\n\n| A |\n| - |\n| 1 |\n$$", "\\[\nx\n\n| A |\n| :-: |\n| 1 |\n\\]",
		"$$\nx^2\n\n| A | B |\n|---|---|\n| 1 | 2 |\n$$\n\nafter", "$$ x $$\n\n| A |\n|---|\n| 1 |",
		"- item\n\n  $$\n  x\n\n  | A |\n  |---|\n  | 1 |", "> $$\n> x\n>\n> | A |\n> |---|\n> | 1 |",
		"$$\nx\n | A |\n | --- |\n | 1 |", "$$\nx\n\n    | A |\n    | --- |", "$$\nx\n\n| A |\n|---\n| 1 |", "$$\nx\n\n|A\n-|-\n1", "$$\nx\n\nA | B\n- | -\n1 | 2",
		"$$\nstream\n\n| A | B |\n|---|", "$$\nstream\n\n| A | B |\n", "$$\nstream\n\n| A | B |",
	}
	compareMarkdownWithPi(t, docs)
}
