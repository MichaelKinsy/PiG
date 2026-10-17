//go:build !race

package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The assistant Markdown transform runs on every Markdown render of a changed assistant block, which is every streaming
// frame. Its transformer list (the Mermaid transformer, then the extensions') changes only with the Mermaid mode, the theme
// or the extension runner, so a render of text with no diagram must not build the list, the Mermaid closure and the
// transform closure again (TU-T6).
func TestAssistantMarkdownTransformDoesNotRebuildItsTransformersPerRender(t *testing.T) {
	m := statusBorderMode(t, false)
	transform := m.assistantMarkdownTransform(nil, extension.MarkdownMessageAssistant, nil)
	const markdown = "An answer in **Markdown** with `code` and no diagram.\n\n- one\n- two\n"
	if got := transform(markdown, 80); got != markdown {
		t.Fatalf("transform changed text with no diagram: %q", got)
	}
	allocs := testing.AllocsPerRun(200, func() { _ = transform(markdown, 80) })
	if allocs != 0 {
		t.Fatalf("assistant Markdown transform allocates %.0f times per render of unchanged state, want 0 (markdownTransformers builds a new list and Mermaid transformer, and CreateMarkdownTransform a new closure, per call)", allocs)
	}
}
