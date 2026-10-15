//go:build !pig_strip_mermaid

package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A message without the word has no Mermaid fence, so the transform returns it with normalized line endings, as the parse
// does; a fence written in another case is still parsed and replaced.
func TestMermaidTransformSkipsOnlyMessagesWithoutTheWord(t *testing.T) {
	context := extension.MarkdownTransformContext{AvailableWidth: 80}
	for _, text := range []string{"plain\r\ntext\rwith ```go\r\nx\r\n```", "```\nmerm aid\n```", "", "~~~MerMaid\ngraph TD\nA-->B\n~~~"} {
		got := transformMermaidBlocks(text, context, nil)
		normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
		if fences := topLevelMermaidFences(normalized); len(fences) == 0 && got != normalized {
			t.Fatalf("%q: got %q, want %q", text, got, normalized)
		}
	}
	if got := transformMermaidBlocks("~~~MerMaid\ngraph TD\nA-->B\n~~~", context, nil); strings.Contains(got, "MerMaid") {
		t.Fatalf("a Mermaid fence in mixed case was not rendered: %q", got)
	}
}
