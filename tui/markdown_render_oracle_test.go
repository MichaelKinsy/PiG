package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type markdownRenderProbe struct {
	Text     string `json:"text"`
	PaddingX int    `json:"paddingX"`
	PaddingY int    `json:"paddingY"`
	Widths   []int  `json:"widths"`
}

// markdownRenderDocs are the documents of every markdown shape the oracle compares.
func markdownRenderDocs() []string {
	return []string{
		"", "plain paragraph", "two\n\nparagraphs", "soft\nbreak and  \nhard break\\\nbackslash break",
		"# H1\n## H2\n### H3\n#### H4\n##### H5\n###### H6", "Setext H1\n=========\n\nSetext H2\n---------",
		"**bold** *italic* ***both*** ~~strike~~ `code` __under__ _it_ **bold with *nested italic* inside**",
		"- one\n- two\n  - nested a\n  - nested b\n    - deeper\n- three", "1. first\n2. second\n   1. sub\n   2. sub\n10. tenth\n11. eleventh",
		"3. starts at three\n4. next", "- [ ] todo\n- [x] done\n- plain", "- loose\n\n- items\n\n  with paragraph\n- end",
		"* star\n+ plus\n- dash", "> quote line\n> second\n>\n> > nested quote\n> back", "> **bold quote**\n> - list in quote\n> ```\n> code in quote\n> ```",
		"```\nplain fence\n  indented\n```", "```go\nfunc main() {}\n```\n\ntext after", "~~~\ntilde fence\n~~~", "    indented code block\n    second line", "```\nunterminated fence\nstill code",
		"---\n\n***\n\n___", "[link](https://example.com) and [titled](https://example.com \"Title\") and <https://auto.example.com> and https://bare.example.com/path.",
		"snake_case_name and 2*3*4 and a * b * c", "foo_bar_ baz and a*b*c and a_b_c", "__x__y _a_b_", "***a** b*", "**a *b***", "*a **b***", "__x__y _a_b_ __under__ _it_", "***both*** and **_mixed_** and *__mixed__*", "*a **b** c* and _a __b__ c_", "**a *b* c** and __a _b_ c__", "**unclosed", "*unclosed", "| a | b | c | d | e | f |\n|---|---|---|---|---|---|\n| 1 | 2 | 3 | 4 | 5 | 6 |", "| Name | Description | Notes |\n|:--|:-:|--:|\n| alpha | a rather long description that must wrap inside its column | n |\n| b | short | a very long note cell that also wraps around |", "| a | b |\n|---|---|\n|  | empty left |\n| empty right | |", "a | b\n--|--\n1 | 2", "| **bold** | `code` | [link](https://x.y) |\n|---|---|---|\n| *em* | ~~s~~ | plain |", "| a | b |\n|---|---|\n| esc \\| pipe | x |", "| a | b |\n|---|---|\n| ragged |\n| too | many | cells |", "| 😀 | 日本語 |\n|---|---|\n| ✓ | ａｂｃ |\n| ⚠️ | e\u0301 |", "text before\n| a | b |\n|---|---|\n| 1 | 2 |\ntext after", "| a |\n|---|\n| " + strings.Repeat("longword", 12) + " |", "| h1 | h2 |\n|----|----|\n| a | b |\n\n| h3 |\n|----|\n| c |", "*[em link](https://x.y)*", "[**bold** link](https://x.y) *a* [b *c*](u) *d*", "*a [b* c](u)", "**[x](u)**y", "a [ *b* ] c *d*", "*foo**bar**baz*", "**foo*bar**", "*foo *bar**", "**foo**bar and __foo__bar and a**b**c", "**\"quoted\"** and *(paren)* and a*\"b\"*c", "_foo_bar_baz and _foo_ bar_", "[**bold** link](https://x.y) and *[em link](https://x.y)*", "`a*b*c` and *`code`* and **`c`**", "\\*not em\\* and \\_not\\_ and *esc\\*aped*", "*a* *b* **c** __d__ _e_", "***a*** ***b** c*** ****", "*foo\nbar* and **foo\nbar**", "** ** and _ _ and a * * b", "*😀* and **日本語** and _é_", "***a**b*c** and *a**b***c*", "**foo *bar** baz*", "###\n\ntext", "# \ntext", "#", "text\n\n# \n\nmore", "## \n- a", "[x][r] and [r][] and [r] and [R] and [nodef] and [x][nodef]\n\n[r]: https://example.com/ref \"Title\"", "[r]: https://a.b\n\ntext [r]", "text\n[r]: https://a.b\nmore", "[r]: <https://a.b/c d>\n\n[r]", "[Foo  Bar]: /u\n\n[foo bar] [FOO BAR][]", "[r]:\n  https://a.b\n\n[r]", "- [r]: https://a.b\n\n[r]", "[a]: https://a.b\n[b]: https://c.d\n\n[a] [b]", "[r]: https://a.b\n[r]", "- [ ] task [r]\n\n[r]: https://a.b", "```\n[r]: https://a.b\n```\n\n[r]", "text\n\n[a]: /a\n[b]: /b", "1. a\n```\nc\n```", "- a\n\n```\nc\n```", "- a\n  - b\n```\nc\n```", "> - l\n> ```\n> c\n> ```", "text\n\n    code after blank\n\n    more\n\nafter", "para\n    not code (lazy)", "- item\n\n    in list\n\n- next", "\tTab indented\n\tsecond", "code\n\n        deeper\n    base", "    code\ntext right after", "    a\n    b\n", "    a\n\n\ntext", "> quote\n\n    code after quote", "a\n\n", "a\n\n\n", "```\nx\n```\n\n", "# h\n\n", "- a\n\n", "> q\n\n", "| a |\n|---|\n| b |\n\n", "a\n  \n", "a\n", "_unclosed and __unclosed", "** spaced ** and * spaced *", "<https://x.y/z?q=1> <mailto:a@b.co> <not a link> <a@b>", "![image](https://example.com/i.png) after", "![alt *em*](u) text", "text\n---\nmore", "para one\nline two\n====", "a\n=", "a\n-", "[t](https://e.com 'Title') and [t2](https://e.com (Paren)) and [t3](https://e.com  \"Spaced\" )", "a [ref link][r] here\n\n[r]: https://example.com/ref", "email <a@b.com> and a@b.com", "http://a.b and www.c.d\n\nx <a href=\"u\">open\n\nhttp://a.b and www.c.d\n\nclose</a>\n\nhttp://a.b and www.c.d",
		"| a | b |\n|---|---|\n| 1 | 2 |", "| left | center | right |\n|:-----|:------:|------:|\n| l | c | r |\n| longer cell text | mid | 1 |",
		"| 名前 | 説明 |\n|---|---|\n| 日本語 | テキスト |\n| 👨‍👩‍👧 | ⚠️ |", "| a |\n|---|\n| only column with a very long cell that exceeds narrow widths |",
		"<div>html block</div>\n\ninline <b>html</b> tag", "escaped \\*stars\\* and \\# hash and \\[brackets\\]", "&amp; &lt; &gt; &copy; entity",
		"日本語の段落です。とても長い文章が折り返される場合の確認をします。English mixed 한국어 text.", "emoji 👍🏽 and 🇺🇸 and 1️⃣ in text\n\n- 😀 list\n- 🚀 items",
		"# Heading with `code` and **bold**\n\ntext", "- item with `code`, **bold**, [link](https://x.y)\n- item two with a very long line that has to wrap around at narrow widths for sure",
		"Term\n: not a definition list\n\ntext", "line one\n\n\n\nline two after many blank lines", "trailing spaces   \n\n   leading spaces", "tab\tin\ttext\n\n\tindented with tab",
		"\\\n", "# \n\n##\n\n#nospace", "1) paren list\n2) second", "a\n- list directly after paragraph\n- second", "**unclosed bold and `unclosed code",
		"$$x^2$$ and $a+b$ latex", "footnote[^1]\n\n[^1]: the note", "<!-- comment -->\n\ntext", "\u200bzero width\u200d joiner and \x1b[31mraw ansi\x1b[0m in text",
		// Space tokens at the start and end of the text, empty headings, closing hash runs and unterminated fences (found by the mutated-document oracle).
		"\ntext", "  \npara", "\n\n- list\n- more", "\n```\ncode\n```", "a\n\n  ", "a\n   ", "- [ ] task\n  ", "- a\n\n\n", "#\n#", "# \n\n##\n", "#\t##\n\next", "# Title ##", "## a #b", "### x ###  ", "# #", "# a \\#",
		"```\nx\n", "```\n\n", "```\n\n\n", "```\nx\n\n\n", "``\nx\n```\n\n",
		// An empty list item ends its list (no lazy continuation), and spaces ending a list's last line are a space token.
		"- \nnext", "-\nnext", "- \n a\n", "- \nlist\n- more", "1. \n```\nc\n```", "- a \n```\nc\n```", "- [ ] x \n", "- \n\n", "- a  \n\nnext", "- a \n- b\n\nx", "- a \nnext",
		// A list item whose first line is a list item holds a nested list with no bullet of its own; an indented line lazily continuing a quoted paragraph loses up to four spaces.
		"- + a", "- - a", "- * *", "- +", "* *", "- 1. a\n  2. b\n- c", "> quote\n    code after quote", "> a\n    b\n    c", "> a\n     b", "> a\n\tb", "> a\n  \tb", "> a\n\t\tb", "- + a\n  - b", "- - a\n- b", "1. - a\n2. b", "- + a\n\n  text", "-     + a", "-      + a", "-    + a", "1.     - a",
		// An indented continuation line of a list item loses up to four spaces; a lazy line after a quoted list item with trailing spaces is a paragraph of its own; the last item's text is trimmed; a text of only definitions or an empty quote followed by blank lines renders its space token.
		"- a\n      code", "- a\n     code", "- a\n       code", "- a\n    code", "> - a  \nb", "> - a \nb", "> - a\nb", "> - a   \nb", "[r]: https://a.b\n\n", "> \n\n",
		// The break test's other arms: a rule or indented code as the item's last line stops a lazy line too.
		"- a\n  ***\nlazy", "- a\n  ---\nlazy", "- a\n\n      code\nlazy", "- a\n  # h\nlazy", "- # h\nlazy", "- \t\nnext", "+ \nnext", "2) \nnext", "- a  \n- b  \n", "- a\n\n  b  \n",
		// An indented code block after a blank line keeps its indentation beyond the four spaces, including after a code line (rev-fix-pi-bugs-session-1).
		"- a\n\n      code\n      more", "- a\n\n      code\n          deeper", "> - a  \n", "> - a  \n\nb", "> 1. a  \nb", "> - a\n> - b  \nc", "- a\n      code\n- b", "- a\n  b\n      code", "[r]: https://a.b\n\n\n", ">\n\n",
		// An empty heading's raw is trimmed of its newlines, so what follows it is a space token when it is longer than one character.
		"#", "#\n", "# \n\n", "#\n ", "#\n\n", "a\n#\n   ", "``\n#\n\n\n", "#\n \n", "# a\n ",
		// A lazy line cannot continue a quote whose last line is empty, and a quote that renders no rows still adds its spacing row.
		">", "> ", ">\nnext", "> \nnext", ">\n\nnext", "> a\n>\nb", "next\n>\nx", ">\n>\n", ">\n> a", ">   \na", ">  \na", ">    \na", "> a\n>\nb", ">\n>   \na",
		// Inside a list item block spacing is suppressed, so an empty quote there adds no spacing row (rev-fix-pi-bugs-session-1).
		"- >\n  a", "- > \n  a", "1. >\n   x", "- a\n  >\n  b", "> - >\n>   a",
		// A lazy line joins the item as written, indentation included; four spaces after the marker already make the first line indented code, which no lazy line continues.
		"- a\n b", "* ) \n a\n", "- a \n b", "1. a\n b", "- a\n  b\n c", "- ![\n a", "-    **a \nnext", "-    a\nnext", "-     a\nnext", "-   a\nnext", "1.    a\nnext", "-  # h\nnext", "-  ***\nnext", "- a\n    ---\nlazy", "- a\n |    code\nlazy", "- a\n   ---\nlazy", "-  a\n   ---\nlazy", "- a\n  # h\nlazy", "1. a\n    ---\nlazy",
		// The lazy-line break test of Tokenizer.ts list looks at the previous line as sliced at the content indent and at the first line as written; only a blank previous line, not a sliced-blank one, ends the item. A lazy line inside a fence stays in the fence.
		"- a\nb  \ny", "-  a\nlaz  \ny", "- a\n\n  b\nlazy", "- a\n  \n  b\nlazy", "- \n  b\nlazy", "- a\n   \nlazy", "- a\nb\n \nc", "- ```\n  \n  x\nlazy", "- ```\n  x\nlazy\n  y", "- ```\n  x\nlazy\n  ```\nafter", "- ```\n  x\n```\nafter", "- ```\n  x\n- b", "- ~~~\n  x\n   lazy", "1. a\n\n   b\nlazy", "- a\n\n  b\n  c\nlazy\n- d",
		// A pending or closed LaTeX block's raw takes the newlines after it, so the text after it is a space token only when longer than one character.
		"$$-\n\n", "$$-`\n\n", "$$a\n\n", "$$\nx\n\n", "$$\nx\n$$\n\n", "$$\nx\n$$\n\n\n", "$$ x $$\n\n\n", "\\[\nx\n\n", "\\[x\\]\n\n\n", "a\n\n$$\nx\n\n",
		// A nested list whose last item is empty but for spaces after its marker leaves a newline that the item's text lexes as a space token.
		"- -\n  a", "- - \n  a", "- - \n-b", "1. - \n   a", "- - \n\n  a", "- - \n  - \n  a", "- + \n  téxt", "- - \n  ```\n  x\n  ```", "- - \n  # h",
		// A lazy line's break test slices it at the content indent in UTF-16 units, so a non-ASCII first character shifts what the next lazy line is tested against.
		"- a\n\u00e9# h\nc", "- a\n\u00e9\u00e9# h\nc", "-  a\n\u00e9```\nc", "- a\n\u65e5---\nc", "1. a\n\u00e9\u00e9\u00e9    x\nc", "- a\n\U0001F600# h\nc",
		strings.Repeat("word ", 60), strings.Repeat("a", 90), "- " + strings.Repeat("longitem ", 20),
	}
}

// Markdown.render and its token renderers against the pinned pi-tui (components/markdown.ts over marked 18): headings, emphasis nesting, lists (nested, ordered,
// task, loose), code spans and fences, block quotes, tables, rules, links, autolinks, HTML, escapes, hard breaks, CJK and emoji, at several widths and paddings,
// with each theme function tagged so every style call is visible.
// Pi source: packages/tui/src/components/markdown.ts
func TestMarkdownRenderMatchesPi(t *testing.T) {
	compareMarkdownWithPi(t, markdownRenderDocs())
}

// compareMarkdownWithPi requires Markdown.render of every document to equal pi-tui's at several widths and paddings.
func compareMarkdownWithPi(t *testing.T, docs []string) {
	t.Helper()
	var probes []markdownRenderProbe
	for _, doc := range docs {
		for _, pad := range [][2]int{{0, 0}, {1, 1}, {2, 0}} {
			probes = append(probes, markdownRenderProbe{Text: doc, PaddingX: pad[0], PaddingY: pad[1], Widths: []int{1, 2, 3, 4, 20, 40, 80}})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/markdown_render.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	tag := func(name string) func(string) string {
		return func(text string) string { return "<" + name + ">" + text + "</" + name + ">" }
	}
	theme := MarkdownTheme{
		Heading: tag("h"), Link: tag("a"), LinkUrl: tag("u"), Code: tag("c"), CodeBlock: tag("cb"), CodeBlockBorder: tag("cbb"),
		Quote: tag("q"), QuoteBorder: tag("qb"), Hr: tag("hr"), ListBullet: tag("lb"), Bold: tag("b"), Italic: tag("i"), Strikethrough: tag("s"), Underline: tag("ul"),
	}
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	t.Cleanup(ResetCapabilitiesCache)
	differing := map[string]bool{}
	for i, probe := range probes {
		md := NewMarkdownWithOptions(probe.Text, probe.PaddingX, probe.PaddingY, &theme, nil, nil)
		for w, width := range probe.Widths {
			got := nonNil(md.Render(width))
			if !reflect.DeepEqual(got, nonNil(expected[i][w])) {
				if !differing[probe.Text] {
					t.Logf("%q paddingX=%d paddingY=%d width=%d:\n got %q\nwant %q", probe.Text, probe.PaddingX, probe.PaddingY, width, got, expected[i][w])
				}
				differing[probe.Text] = true
			}
		}
	}
	for _, doc := range docs {
		if differing[doc] {
			t.Errorf("markdown differs from Pi: %q", doc)
		}
	}
}
