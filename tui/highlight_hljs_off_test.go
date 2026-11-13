//go:build pig_strip_syntax_highlight

package tui

import (
	"slices"
	"strings"
	"testing"
)

// A build that strips syntax-highlight draws a go code block as Pi draws a block of a language highlight.js does not support
// (highlight_hljs_off.go): every line of the code in the theme's mdCodeBlock color and no token colored, as the lines of
// HighlightCode and as the rows of a Markdown, which are the rows of the same code under a language that does not exist.
// The stock build's counterpart is TestRuntimeStripSyntaxHighlightRendersUnknownLanguagePath (highlight_strip_test.go).
func TestStrippedBuildDrawsAGoCodeBlockAsAnUnknownLanguage(t *testing.T) {
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	const code = "func main() {\n\treturn \"x\" // done\n}"
	wantLines := []string{
		"\x1b[38;2;104;183;141mfunc main() {\x1b[39m",
		"\x1b[38;2;104;183;141m\treturn \"x\" // done\x1b[39m",
		"\x1b[38;2;104;183;141m}\x1b[39m",
	}
	if got := HighlightCode(code, "go"); !slices.Equal(got, wantLines) {
		t.Errorf("HighlightCode(go) = %q, want %q", got, wantLines)
	}
	render := func(lang string) []string { return NewMarkdown("```" + lang + "\n" + code + "\n```").Render(30) }
	wantRows := []string{
		"\x1b[38;2;157;165;169m```go\x1b[39m                         ",
		"  \x1b[38;2;104;183;141mfunc main() {\x1b[39m               ",
		"  \x1b[38;2;104;183;141m   return \"x\" // done\x1b[39m       ",
		"  \x1b[38;2;104;183;141m}\x1b[39m                           ",
		"\x1b[38;2;157;165;169m```\x1b[39m                           ",
	}
	got := render("go")
	if !slices.Equal(got, wantRows) {
		t.Errorf("the go code block renders %q, want %q", got, wantRows)
	}
	if unknown := strings.ReplaceAll(strings.Join(render("zz"), "\n"), "```zz", "```go"); strings.Join(got, "\n") != unknown {
		t.Errorf("the go code block renders %q, a block of an unknown language %q", got, unknown)
	}
}
