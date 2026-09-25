package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestMermaidBlockWiring pins the integration seam: a ```mermaid block set on an
// AssistantMessageBlock is replaced by a rendered diagram at the block's render
// width via the Markdown.Transform hook, and the MermaidRenderingMode gating
// applies. Themed output is not byte-comparable to pi (pig's theme colours
// differ), so this asserts the structural outcome; the transform itself is
// byte-verified against pi in TestMermaidTransformMatchesUpstream.
func TestMermaidBlockWiring(t *testing.T) {
	const src = "Here is a diagram:\n\n```mermaid\ngraph TD\n  A[Start] --> B[End]\n```\n\nDone."

	renderWith := func(mode string, streaming bool) string {
		transformers := []extension.MarkdownTransformer{
			createMermaidMarkdownTransformer(func() string { return mode }, tui.ActiveTheme()),
		}
		block := tui.NewAssistantMessageBlock(false)
		block.SetMarkdownTransform(createMarkdownTransform(extension.MarkdownMessageAssistant, streaming, transformers))
		block.SetTextDelta(src)
		return strings.Join(block.Render(80), "\n")
	}

	rawLeaked := func(out string) bool {
		return strings.Contains(out, "```mermaid") || strings.Contains(out, "graph TD")
	}
	hasDiagram := func(out string) bool { return strings.ContainsAny(out, "┌└─▼│") }

	// streaming mode (default): diagram renders, surrounding markdown intact.
	out := renderWith("streaming", false)
	if rawLeaked(out) {
		t.Errorf("streaming: raw mermaid source leaked into rendered block:\n%s", out)
	}
	if !hasDiagram(out) {
		t.Errorf("streaming: diagram not rendered (no box-drawing):\n%s", out)
	}
	if !strings.Contains(out, "Here is a diagram:") || !strings.Contains(out, "Done.") {
		t.Errorf("streaming: surrounding markdown lost:\n%s", out)
	}

	// mode off: the fence passes through untouched (raw source stays).
	if out := renderWith("off", false); !rawLeaked(out) {
		t.Errorf("off: expected raw mermaid source to pass through, got:\n%s", out)
	}

	// final mode while streaming: diagram is NOT rendered yet (raw stays);
	// once streaming ends it renders.
	if out := renderWith("final", true); !rawLeaked(out) {
		t.Errorf("final+streaming: expected raw source (deferred render), got:\n%s", out)
	}
	if out := renderWith("final", false); rawLeaked(out) || !hasDiagram(out) {
		t.Errorf("final+complete: expected rendered diagram, got:\n%s", out)
	}
}
