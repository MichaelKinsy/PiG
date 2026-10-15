//go:build !pig_strip_mermaid

package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/mermaid.ts

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/test/mermaid.test.ts:65-71. A class suffix attaches style metadata; it does not terminate the flowchart link.
func TestMermaidClassAssignmentsUpstream(t *testing.T) {
	markdown := "```mermaid\nflowchart LR\n  A[Foo]:::highlight --> B[Bar]\n```"
	transform := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)
	rendered := transform(markdown, extension.MarkdownTransformContext{AvailableWidth: 100, MessageType: extension.MarkdownMessageAssistant})
	if strings.Contains(rendered, "```mermaid") || strings.Contains(rendered, "Mermaid diagram not rendered") || !strings.Contains(rendered, "│ Foo ├───▶│ Bar │") {
		t.Fatalf("class assignment lost its link: %q", rendered)
	}
}
