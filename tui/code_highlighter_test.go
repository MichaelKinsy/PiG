package tui

import (
	"slices"
	"testing"
)

func sameLines(a, b []string) bool { return len(a) > 0 && len(a) == len(b) && &a[0] == &b[0] }

// Ports the CodeHighlighter test of packages/coding-agent/test/code-highlighter.test.ts: "reuses its last result until
// the code, the theme or the grammars change".
func TestCodeHighlighterReusesItsLastResultUntilTheCodeTheThemeOrTheGrammarsChange(t *testing.T) {
	original := ActiveTheme().Name
	t.Cleanup(func() { SetTheme(original) })
	SetTheme("dark")
	code := "const page = html`<div class=\"x\">ok</div>`;"
	var highlighter CodeHighlighter
	first := highlighter.Highlight(code, "javascript")
	if !sameLines(highlighter.Highlight(code, "javascript"), first) {
		t.Fatal("the same code was highlighted again")
	}
	if sameLines(highlighter.Highlight("const a = 1;", "javascript"), first) {
		t.Fatal("other code was served the old lines")
	}
	if sameLines(highlighter.Highlight(code, "typescript"), first) {
		t.Fatal("another language was served the old lines")
	}

	SetTheme("light")
	if got, want := highlighter.Highlight(code, "javascript"), HighlightCode(code, "javascript"); !slices.Equal(got, want) {
		t.Fatalf("after a theme change\n got %q\nwant %q", got, want)
	}
	light := highlighter.Highlight(code, "javascript")
	if !sameLines(highlighter.Highlight(code, "javascript"), light) {
		t.Fatal("the same code was highlighted again after the theme change")
	}
	// Loading the deferred grammars registers languages the lines may embed (HTML in a template literal), which
	// LoadAllHighlightLanguages announces by a new generation. The process loads them once, so the test announces it.
	hlMu.Lock()
	hlGeneration++
	hlMu.Unlock()
	if sameLines(highlighter.Highlight(code, "javascript"), light) {
		t.Fatal("the lines outlived a grammar load")
	}
}
