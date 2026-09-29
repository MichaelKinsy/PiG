package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi mermaid.ts:76-77 preserves rejected input rather than synthesizing a diagnostic. Keep the grammar and overflow inputs from the retired D50 cases at this boundary.
func TestMermaidRejectedInputRemainsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		width      int
	}{
		{"semicolon inside statement", "sequenceDiagram\n  A->>B: seq [1;1:1A]", 200},
		{"unrecognized type", "gitGraph\n  commit", 200},
		{"incomplete message", "sequenceDiagram\n  A->>", 200},
		{"oversized sequence", "sequenceDiagram\n  participant Alice\n  participant Bob\n  Alice->>Bob: hello there", 12},
		{"unmeasured width", "sequenceDiagram\n  A->>B: hello", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			md := "```mermaid\n" + tc.body + "\n```"
			for _, streaming := range []bool{false, true} {
				got := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)(md, extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, IsStreaming: streaming, AvailableWidth: tc.width})
				if got != md {
					t.Fatalf("streaming=%v changed source: got %q, want %q", streaming, got, md)
				}
			}
		})
	}
}

func TestMermaidValidSequenceRendersWithoutWarning(t *testing.T) {
	for _, body := range []string{"sequenceDiagram\n  A->>B: hi;", "sequenceDiagram\n  A->>B: hello"} {
		md := "```mermaid\n" + body + "\n```"
		out := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)(md, extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 200})
		if out == md || !strings.ContainsAny(out, "┌└─▶│") || strings.Contains(out, "Mermaid diagram not rendered") {
			t.Fatalf("valid sequence did not render cleanly: %q", out)
		}
	}
}
