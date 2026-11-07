package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// markdownRowIsOneOverWideGrapheme reports whether a row wider than width is the one case Pi's Markdown emits it: the row's text is a single grapheme whose own
// width exceeds the width. Pi's wrapTextWithAnsi breaks a long word with breakLongWord (packages/tui/src/utils.ts), which starts a row, then adds the grapheme
// whatever its width (currentLine += grapheme, utils.ts:1144-1145 at the pin); at width 1 a double-width character such as 툚 or 😀 is therefore a row of 2 columns
// in Pi too (TestMarkdownRenderMatchesPi compares those rows). Pi's main screen then throws on such a row (tui-main-screen.ts:541), so a terminal one column wide
// cannot show a double-width character in either implementation.
func markdownRowIsOneOverWideGrapheme(line string, width int) bool {
	cluster, rest := widthx.FirstGrapheme(widthx.StripTerminalSequences(line))
	return cluster != "" && rest == "" && widthx.GraphemeWidth(cluster) > width
}

// A Markdown component renders without panicking, and no line it returns is wider than the width it was given, except a row that holds one grapheme wider than the
// width, which Pi emits as it is (markdownRowIsOneOverWideGrapheme): the renderer throws on any other wider line.
func FuzzMarkdownRenderStaysWithinWidth(f *testing.F) {
	for _, seed := range []string{
		"# Title\n\ntext **bold** _it_ `code`\n\n- a\n- b\n  - c\n\n1. x\n2. y\n", "| a | b |\n|---|---|\n| 1 | 2 |\n| long cell content here | 日本語 |\n",
		"```go\nfunc main() {}\n```\n", "> quote\n> > nested\n", "[link](https://example.com/very/long/url/that/wraps) ![img](x.png)\n", "---\n***\n", "a\tb\n\n\ttabbed\n",
		"$$x^2 + \\frac{1}{2}$$ and $y$\n", "😀 emoji 👨‍👩‍👧 family e\u0301 combining\n", "<div>html</div> &amp; &#x41;\n", "\x1b[31mred\x1b[0m ansi in text\n", "- [ ] task\n- [x] done\n",
	} {
		f.Add(seed, 40)
	}
	f.Fuzz(func(t *testing.T, content string, width int) {
		if width < 0 {
			width = -width
		}
		width = 1 + width%120
		lines := NewMarkdown(content).Render(width)
		for i, line := range lines {
			if w := widthx.VisibleWidth(line); w > width && !markdownRowIsOneOverWideGrapheme(line, width) {
				t.Fatalf("line %d is %d columns wide, width is %d: %q\ncontent %q", i, w, width, line, content)
			}
		}
	})
}
