package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestMarkdownDisplayMathWiderThanTheContentWidthFallsBackToRawSource ports packages/tui/test/markdown.test.ts "falls back to
// raw source when display math is wider than the content width" (cd1ea342, #11483): Markdown with no padding, ANSI stripped, trimEnd per line.
func TestMarkdownDisplayMathWiderThanTheContentWidthFallsBackToRawSource(t *testing.T) {
	source := strings.Join([]string{
		"\\[",
		`\frac{\partial^2 u}{\partial x^2} + \frac{\partial^2 u}{\partial y^2} = \sum_{i=1}^{n} \frac{1}{i^2} + \int_0^1 f(x)\,dx`,
		"\\]",
	}, "\n")
	render := func(width int) []string {
		return markdownPlainLines(NewMarkdownWithOptions(source, 0, 0, nil, nil, nil).Render(width))
	}

	if got, want := render(40), []string{
		"∂² u   ∂² u   n   1   1",
		"──── + ──── = ∑   ── +∫ f(x) dx",
		"∂ x²   ∂ y²  i=1  i²  0",
	}; !slices.Equal(got, want) {
		t.Fatalf("width 40: got %q, want %q", got, want)
	}

	narrow := render(30)
	if narrow[0] != "\\[" {
		t.Errorf("first row %q, want \\[", narrow[0])
	}
	if last := narrow[len(narrow)-1]; last != "\\]" {
		t.Errorf("last row %q, want \\]", last)
	}
	for _, line := range narrow {
		if strings.Contains(line, "─") {
			t.Errorf("row %q keeps the fraction bar", line)
		}
		if len([]rune(line)) > 30 {
			t.Errorf("row %q is wider than 30", line)
		}
	}
}

// TestMarkdownDisplayMathWidthFallbackMatchesPi renders display math at widths that straddle the 2D layout width, at the top level,
// in a quote and in a list item, with both delimiters, closed and unclosed, in the live Pi so Pi decides.
func TestMarkdownDisplayMathWidthFallbackMatchesPi(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "extensions", "sdk-ts", "node_modules")); err != nil {
		t.Skip("the Pi oracle is not installed (make parity-deps)")
	}
	equation := `\frac{\partial^2 u}{\partial x^2} + \frac{\partial^2 u}{\partial y^2} = \sum_{i=1}^{n} \frac{1}{i^2} + \int_0^1 f(x)\,dx`
	docs := []string{
		"\\[\n" + equation + "\n\\]",
		"$$\n" + equation + "\n$$",
		"$$" + equation + "$$",
		"Before\n\n\\[\n" + equation + "\n\\]\n\nAfter",
		"> \\[\n> " + equation + "\n> \\]",
		"- item\n\n  $$\n  " + equation + "\n  $$",
		"\\[\n" + equation,
		"$$\n" + equation,
		"$$\n\\frac{1}{2}\n$$",
		"$$\nx^{n^2}\n$$",
		"$$\n\\begin{pmatrix} a & b \\\\ c & d \\end{pmatrix}\n$$",
		"$$\n" + strings.Repeat("x + ", 30) + "y\n$$",
	}
	compareMarkdownWithPi(t, docs)
}
