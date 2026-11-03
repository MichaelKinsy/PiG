package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Pi-oracle sweeps of the Markdown renderers (markdown.ts render, renderBlock, renderToken, renderInlineTokens, renderList, renderTable) over inputs the
// variant corpus of markdown_contract_oracle_test.go reaches at only five widths: every table width from zero to the width where nothing wraps, block
// shapes at the boundaries of each branch of the block loop (ATX and fence openers, indented code, spacing, HTML, definitions, LaTeX, setext and table
// edges, lazy continuation) at many narrow widths, and inline compositions (nested emphasis, code, links, images, escape codes, hard breaks) in each
// context that styles text, under the stock themes the stacked inline writer handles and under the default text styles.

// markdownSweepTables are tables of one to four columns whose cells are short, empty, long, unbroken, wide, emphasised, escaped and missing.
func markdownSweepTables() []string {
	cells := []string{"a", "", "word", "two words here", strings.Repeat("x", 35), "日本語のセル", "🙂 e", "**b** `c`", "a \\| b", "long cell text that wraps over several rows when narrow", "[l](http://u.v)", "a\x1b[31mb\x1b[0m"}
	var docs []string
	for cols := 1; cols <= 4; cols++ {
		align := make([]string, cols)
		header := make([]string, cols)
		for c := range cols {
			align[c] = []string{"---", ":--", "--:", ":-:"}[c%4]
			header[c] = cells[(c*5+1)%len(cells)]
		}
		for shift := range 6 {
			var b strings.Builder
			fmt.Fprintf(&b, "| %s |\n| %s |\n", strings.Join(header, " | "), strings.Join(align, " | "))
			for r := range 3 {
				row := make([]string, 0, cols)
				n := cols
				switch (shift + r) % 4 {
				case 1:
					n = max(cols-1, 0)
				case 2:
					n = cols + 1
				}
				for c := range n {
					row = append(row, cells[(shift*7+r*3+c*5)%len(cells)])
				}
				fmt.Fprintf(&b, "| %s |\n", strings.Join(row, " | "))
			}
			docs = append(docs, b.String(), b.String()+"\nafter", "before\n\n"+b.String()+"> q")
		}
	}
	return append(docs, "|a|\n|-|", "a|b\n-|-\nc|d", "|a|b|\n|-|-|\n|1|", "|a|\n|-|\n|1|2|3|", "| a |\n| - |\n| `x|y` |", "|a|\n|-|\n\ntext", "|a|\n|-|\ntext", "|a|\n|-|\n| b |\n---", "|a|\n|-|\n| b |\n```\nx\n```", "|a|\n|-|\n| b |\n# h", "|a|\n|-|\n| b |\n- l", "text\n|a|\n|-|", "> |a|b|\n> |-|-|\n> |long cell|c|", "- |a|b|\n  |-|-|\n  |long cell|c|", "|a|b|c|d|e|f|g|h|\n|-|-|-|-|-|-|-|-|\n|1|2|3|4|5|6|7|8|", "| |\n|-|\n| |", "|||\n|-|-|\n|||")
}

// TestMarkdownTableWidthSweepMatchesPi renders every table at each width from 0 to 70, with and without padding, in the default, dark-theme and default-color forms.
func TestMarkdownTableWidthSweepMatchesPi(t *testing.T) {
	widths := make([]int, 0, 71)
	for w := 0; w <= 70; w++ {
		widths = append(widths, w)
	}
	var probes []markdownContractProbe
	for _, doc := range markdownSweepTables() {
		for _, apply := range []func(*markdownContractProbe){
			func(*markdownContractProbe) {},
			func(p *markdownContractProbe) { p.Theme = "dark" },
			func(p *markdownContractProbe) { p.Style = &markdownContractStyle{Color: true, Bold: true} },
		} {
			for _, padX := range []int{0, 1} {
				probe := markdownContractProbe{Text: doc, PaddingX: padX, PaddingY: padX, Theme: "tagged", Ops: markdownContractRenderOps(widths...)}
				apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// markdownSweepBlocks are documents at the edges of the block loop's branches.
func markdownSweepBlocks() []string {
	return []string{
		// ATX headings.
		"#", "# ", "#a", "####### seven", "   # three", "    # four", "# h #", "# h \\#", "# h ##  ", "## h#", "# #", "#\ttab", "###### six", "# a # b #", "\\# not", "#!x", "# h\ntext", "# h\n\n\n# i", "#\n\ntext", "# \n# \n", "### \n\n#### ", "> #", "- #", "> # h\n> text",
		// Fences.
		"```", "``` a", "~~~\nx\n~~~", "~~~~\nx\n~~~", "````\n```\n````", "   ```\n   x\n   ```", "    ```\nx", "``\nx\n``", "```a`b\nx\n```", "~~~ a`b\nx\n~~~", "```\nx\n``", "```\nx\n````", "```\nx", "```\nx\n", "```\nx\n\n",
		" ```\n  x\n   y\n  ```", "  ```\n    x\n  ```", "   ```\n x\n```", "> ```\n> x", "- ```\n  x", "```\n\n\nx\n\n```", "1. ```\n   a\n   ```", "```\n``", "```\n`", "~~~\n~~", "```js\nlet a = 1\n```\ntext", "text\n```\ncode\n```\ntext", "```\na\n```\n```\nb\n```",
		// Indented code.
		"    a\n     b\n    c", "    a\n\n    b", "para\n    not code", "    a\n\n\n    b", "    \n    a", "- a\n\n      code", "> a\n>\n>     code", "    a\ntext", "text\n\n    a\n\ntext", "     five", "   three\n    four",
		// Spacing, rules and HTML.
		"a\n\n\n\nb", "a\n   \nb", "---\n---", "a\n---\nb", "***\n___", "- - -", "a\n\n---\n\nb", "> a\n\n\n> b", "> a\n> b\n\n> c", "<div>a</div>\nb", "<div>\na\n\nb\n</div>\n\n```\nx\n```", "<!-- c -->\n```\nx\n```", "<p>x</p>\n\n\ny", "<br>", "x <br> y", "<div>\n</div>\n- a", "<div>x</div>\n\n---",
		// Definitions.
		"[a]: http://x\n\n[a]", "[a]: http://x\n[a]: http://y\n\n[a]", "[a]: <http://x> 'title'\ntext [a]", "> [a]: http://x\n\n[a]", "[a]: http://x\n[b]: http://y", "[a]:\nhttp://x\n\n[a]", "[a]: http://x\n\ntext\n\n[A]", "text\n\n[a]: http://x", "[a]: http://x\n\n\n\nz", "- [a]: http://x\n\n[a]", "[a]: http://x\n[b]: http://y\n\ntext", "[a]: http://x\n\n# h",
		// LaTeX.
		"$$\nx\n$$", "$$x$$", "\\[x\\]", "\\[\nx^2\n\\]\ntext", "text\n$$\nx\n$$\nmore", "a $b$ c", "$$ a\nb $$", "😀$$x$$", "é\\[x\\]", "$$\n\\frac{a}{b}\n$$\n\n$$\n\\sqrt{x}\n$$", "$$\\sum_{i=1}^{n} i^2 = \\frac{n(n+1)(2n+1)}{6}$$", "$$\n$$", "$$", "> $$\n> x\n> $$", "- $$\n  x\n  $$", "$$x$$\n- a", "- a\n$$x$$",
		// A paragraph followed by a block.
		"a\n# h", "a\n> q", "a\n- b", "a\n1. b", "a\n2. b", "a\n```\nb\n```", "a\n---", "a\n<div>", "a\n$$x$$", "a\n    b", "a\n|b|\n|-|", "a  \n- b", "   \n- a", "a\n   - b", "a\n    - b", "a\n- \nb", "a\n*\nb", "a\n+ b", "a\n1) b", "a\n10. b", "a\n1234567890. b",
		// Setext.
		"a\nb\n===", "  a\n===", "a\n  ===", "a\n    ===", "> a\n> ===", "- a\n  ===", "a\n- - -\n", "a\n= =", "a\n==", "\\a\n--", "a\n---\n", "a\n-\nb", "a\n=\n\nb", "> a\n---", "a\nb\n--\nc",
		// Quotes and lazy lines.
		">", "> ", ">a", "> a\nb", "> a\n\nb", "> > a\n> b\nc", "> > a\n>\n> b", ">\n>\n>a", "> - a\n> - b\nlazy", "> a\n    lazy\nz", "> a\n- b", "> a\n---", "> a\n# h", "> a\n```\nx\n```", "> ```\n> x\n> ```\nz", "> 1. a\n> 2. b\n\n> c", "> a\n> \n> b", "> a\n>\n\n> b", ">>>> deep", "> > > > > > > > x", "> a\n> b  \n> c", ">     code\nlazy", "> a\n>     b",
		// Lists.
		"- a", "- a\n- b", "* a\n+ b", "1. a\n2. b", "3. a\n1. b", "0. a\n1. b", "1) a\n2) b", "- a\n\n- b", "- a\n\n  b\n- c", "- a\n  - b\n    - c\n      - d", "- a\n - b\n  - c\n   - d\n    - e", "- [ ] a\n- [x] b\n- [X] c\n- [] d\n- [ ]e", "1. a\n   1. b\n      1. c", "- a\n\n\n- b", "- \n  a", "-\n  a", "- a\n- \n- b", "1. \n2. b", "- a\nb\n- c", "- a\n  b\n c", "10. a\n11. b\n100. c", "- a\n   b\n    c\n     d", "- a\n\n      code\n- b", "- > q\n  > r", "- # h\n  text", "- ---\n- ***", "- a\n  ---\n- b", "-\n-\n-", "- a\n\n  > q\n\n  ```\n  c\n  ```\n\n  | h |\n  |---|\n  | c |", "1. a\n\n   b\n2. c", "- a\n* b\n+ c", "1. a\n- b\n2. c", "- a\n1. b\n- c", "text\n- a\n\n- b\ntext", "- a\n\ntext\n\n- b", "- a\n    - b\n  - c", "-  two spaces", "-   three spaces", "-    four\n     b", "- \ta\n", "-\ta", "1.\ta", "- a\n\t- b",
		"1. one\n   two\n\n   three\n2. next", "- a\n  ```\n  code\n  ```\n- b", "- a\n\n  ```\n  code\n\n  more\n  ```\n- b", "- a  \n  b", "- a\\\n  b",
	}
}

// TestMarkdownBlockSweepMatchesPi renders markdownSweepBlocks at narrow, boundary and wide widths with the default constructor, a background style, preserved
// list markers and the dark theme.
func TestMarkdownBlockSweepMatchesPi(t *testing.T) {
	var probes []markdownContractProbe
	for _, doc := range markdownSweepBlocks() {
		for _, apply := range []func(*markdownContractProbe){
			func(*markdownContractProbe) {},
			func(p *markdownContractProbe) { p.Style = &markdownContractStyle{Bg: true, Color: true} },
			func(p *markdownContractProbe) { p.Options.PreserveOrderedListMarkers = true },
			func(p *markdownContractProbe) { p.Theme = "dark"; p.Highlight = false },
		} {
			for _, pad := range [][2]int{{0, 0}, {1, 1}} {
				probe := markdownContractProbe{Text: doc, PaddingX: pad[0], PaddingY: pad[1], Theme: "tagged", Ops: markdownContractRenderOps(0, 1, 2, 3, 4, 5, 6, 8, 11, 16, 24, 80)}
				apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// markdownSweepInlines are inline compositions: emphasis in every nesting, code spans, links, images, escape codes (complete, partial, closes of the emphasis
// codes), hard and soft breaks, carriage returns, entities, raw HTML, LaTeX and empty delimiters.
func markdownSweepInlines() []string {
	return []string{
		"**a**", "*a*", "~~a~~", "**a *b* c**", "***a***", "*a **b***", "~~a **b** c~~", "**a\nb**", "**a  \nb**", "`c`", "`c`**a**", "**`c`**", "[l](u)", "**[l](u)**", "[**l**](u)", "![i](u)", "**![i](u)**", "**a**b**c**",
		"**\x1b[0m**", "**\x1b[22m**", "*\x1b[23m*", "~~\x1b[29m~~", "**a\x1b[22mb**", "\x1b[1m**a**", "**a**\x1b[22m", "\x1b[", "**\x1b[**", "**\x1b[1**", "**\x1b[1;**m", "**\x1b[1;31mred**", "**\x1b[**m", "**\x1b**", "**\x1b[0;mx**", "*a\x1b[23m b*", "~~a\x1b[29m b~~", "**a\x1b[22m**\x1b[22m", "*\x1b[22m*", "**\x1b[3m**",
		"**\x1b]8;;u\x07a\x1b]8;;\x07**", "[\x1b[1ma](u)", "`\x1b[22m`", "`a\x1b[0m` **b**", "**`\x1b[22m`**", "**a\x1b[22m\nb**", "**a\nb\x1b[22m**",
		"a\r\nb", "**a\r\nb**", "**a\rb**", "**a\r", "a\rb", "*a\r\n*", "**\r\n**",
		"&amp; **&lt;**", "<b>x</b> **y**", "$x$ **$y$**", "\\*esc\\* **a**", "**a**\n**b**", "**a**  \n**b**", "text **", "** **", "****", "~~~~", "**  **", "* *", "**a** **b**", "_a_ __b__ ___c___", "**a\n\nb**", "*a**b*c**", "***a** b*", "*a **b* c**", "**a**\\\n**b**", "a\\\nb", "**a \\\n b**",
		"**bold *italic ~~strike `code` x~~ y* z**", "~~*a* **b**~~", "*~~**a**~~*", "**[~~a~~](u) *b*** c", "[a](u) [b](v) **[c](w)**", "<http://a.b> **<c@d.e>**", "http://a.b **http://c.d**", "**a http://b.c d**", "**日本語** *日本語* ~~日本語~~", "**🙂** *🙂*", "***", "*a*b*c*", "_a_b_c_", "__a__b__c__", "**a**\n\n**b**",
		"a  b   c", "a\tb", "  lead **x**  ", "**x**  ", "x  \n", "[a]\n\n[a]: u", "[a][b] **[c][d]**\n\n[b]: u\n[d]: v", "![a][b]\n\n[b]: u", "**\\***", "\\**a*\\*", "`` ` ``", "**``a``**",
	}
}

// TestMarkdownInlineSweepMatchesPi renders markdownSweepInlines as a paragraph, a heading, a quote line, a list item and table cells, under the stock dark and
// light themes, the tagged theme and the default text styles, at a narrow, a medium and a wide width.
func TestMarkdownInlineSweepMatchesPi(t *testing.T) {
	contexts := []string{"%s", "# %s", "###### %s", "> %s", "- %s", "1. %s", "| h | %s |\n|---|---|\n| %s | b |", "- [ ] %s", "> - %s"}
	var probes []markdownContractProbe
	for _, inline := range markdownSweepInlines() {
		for _, format := range contexts {
			doc := strings.ReplaceAll(format, "%s", inline)
			for _, apply := range []func(*markdownContractProbe){
				func(p *markdownContractProbe) { p.Theme = "dark" },
				func(p *markdownContractProbe) { p.Theme = "light" },
				func(p *markdownContractProbe) {},
				func(p *markdownContractProbe) { p.Theme, p.Style = "dark", &markdownContractStyle{Color: true} },
				func(p *markdownContractProbe) {
					p.Theme, p.Style = "dark", &markdownContractStyle{Bold: true, Italic: true, Strikethrough: true, Underline: true}
				},
				func(p *markdownContractProbe) { p.Style = &markdownContractStyle{Color: true, Bg: true} },
			} {
				probe := markdownContractProbe{Text: doc, PaddingX: 1, PaddingY: 0, Theme: "tagged", Ops: markdownContractRenderOps(6, 20, 80)}
				apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
