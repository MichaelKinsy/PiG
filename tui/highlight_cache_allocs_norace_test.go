//go:build !race && !pig_strip_syntax_highlight

package tui

import "testing"

// TestHighlightCodeMemoHitCopiesOnlyTheLines counts what a memo hit costs now that the caller owns its lines
// (TestHighlightCodeLinesBelongToTheCaller): one allocation, the copy of the line slice; the highlighted strings are
// shared, since a Go string cannot be written into.
func TestHighlightCodeMemoHitCopiesOnlyTheLines(t *testing.T) {
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	SetTheme("dark")
	code := "func f(x int) int {\n\treturn x*2 + 1 // doubled\n}"
	for _, highlight := range []func(code, lang string) []string{HighlightCode, highlightMarkdownCode} {
		want := highlight(code, "go")
		got := highlight(code, "go")
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("line %d: a hit returned %q, the miss %q", i, got[i], want[i])
			}
		}
		if allocs := testing.AllocsPerRun(100, func() { _ = highlight(code, "go") }); allocs != 1 {
			t.Fatalf("a memo hit allocates %v times, want 1 (the caller's copy of the lines)", allocs)
		}
	}
}
