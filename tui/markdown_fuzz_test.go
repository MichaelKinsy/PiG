package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// A Markdown component renders without panicking, and no line it returns is wider than the width it was given: the renderer throws on such a line.
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
			if w := widthx.VisibleWidth(line); w > width {
				t.Fatalf("line %d is %d columns wide, width is %d: %q\ncontent %q", i, w, width, line, content)
			}
		}
	})
}
