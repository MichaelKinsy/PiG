//go:build !pig_strip_syntax_highlight

package tui

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A pig_strip_syntax_highlight build of cmd/pig compiles highlight_hljs_on.go out, so it links neither highlight.js nor
// the JavaScript sort only highlight.js uses; stock links both. Both builds link regexp2: the Markdown component
// evaluates marked's rules, which need lookaround and backreferences, with it.
func TestStripSyntaxHighlightBuildOmitsHljs(t *testing.T) {
	if testing.Short() {
		t.Skip("go list of cmd/pig in -short mode")
	}
	deps := func(tags ...string) []string {
		args := append(append([]string{"list"}, tags...), "-deps", "github.com/MichaelKinsy/PiG/cmd/pig")
		out, err := exec.Command("go", args...).Output()
		if err != nil {
			t.Fatalf("go %s: %v", strings.Join(args, " "), err)
		}
		return strings.Fields(string(out))
	}
	stock := deps()
	stripped := deps("-tags", pigstrip.Tag(pigstrip.SyntaxHighlight))
	for _, pkg := range []string{"github.com/MichaelKinsy/PiG/tui/internal/hljs", "github.com/MichaelKinsy/PiG/internal/jsarray"} {
		if !slices.Contains(stock, pkg) {
			t.Errorf("the stock cmd/pig does not link %s", pkg)
		}
		if slices.Contains(stripped, pkg) {
			t.Errorf("a %s build of cmd/pig links %s", pigstrip.Tag(pigstrip.SyntaxHighlight), pkg)
		}
	}
}

// A Piglet that strips syntax-highlight renders a go code block exactly as Pi renders a language highlight.js does not
// support; stock highlights it.
func TestRuntimeStripSyntaxHighlightRendersUnknownLanguagePath(t *testing.T) {
	const code = "func main() {\n\treturn \"x\" // done\n}"
	render := func(lang string) string {
		return strings.ReplaceAll(strings.Join(NewMarkdown("```"+lang+"\n"+code+"\n```").Render(80), "\n"), "```zz", "```go")
	}
	unknown := HighlightCode(code, "zz")
	unknownMarkdown := render("zz")
	if got := HighlightCode(code, "go"); slices.Equal(got, unknown) {
		t.Fatalf("stock go code renders like an unknown language: %q", got)
	}
	if got := render("go"); got == unknownMarkdown {
		t.Fatalf("stock go code block renders like an unknown language: %q", got)
	}

	undo := pigstrip.Strip(pigstrip.ListFeatures, pigstrip.SyntaxHighlight)
	t.Cleanup(undo)
	if got := HighlightCode(code, "go"); !slices.Equal(got, unknown) {
		t.Fatalf("stripped HighlightCode(go) = %q, want the unknown-language rendering %q", got, unknown)
	}
	if got := render("go"); got != unknownMarkdown {
		t.Fatalf("stripped go code block =\n%s\nwant the unknown-language rendering\n%s", got, unknownMarkdown)
	}
	if SupportsLanguage("go") {
		t.Fatal("stripped SupportsLanguage(go) = true")
	}
	if _, err := Highlight(code, HighlightOptions{Language: "go"}); err == nil || err.Error() != `Unknown language: "go"` {
		t.Fatalf("stripped Highlight(go) error = %v, want Unknown language", err)
	}
	if got, err := Highlight("a < b", HighlightOptions{}); err != nil || got != "a < b" {
		t.Fatalf("stripped auto Highlight = %q, %v; want the plain code", got, err)
	}

	undo()
	if got := HighlightCode(code, "go"); slices.Equal(got, unknown) {
		t.Fatalf("unstripped go code renders like an unknown language: %q", got)
	}
}
