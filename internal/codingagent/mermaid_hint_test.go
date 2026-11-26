//go:build !pig_strip_mermaid

package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi mermaid.ts:80-100 preserves unrecognized diagrams and streaming oversized diagrams, and appends a width note for completed oversized diagrams.
func TestMermaidRejectedInputRemainsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		width      int
		wantNote   string
	}{
		{"unrecognized type", "gantt\n  title Plan", 200, ""},
		{"oversized sequence", "sequenceDiagram\n  participant Alice\n  participant Bob\n  Alice->>Bob: hello there", 12, "Mermaid diagram not rendered: needs 21 columns, 12 available"},
		{"unmeasured width", "sequenceDiagram\n  A->>B: hello", 0, "Mermaid diagram not rendered: needs 12 columns, 0 available"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			md := "```mermaid\n" + tc.body + "\n```"
			for _, streaming := range []bool{false, true} {
				got := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)(md, extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, IsStreaming: streaming, AvailableWidth: tc.width})
				want := md
				if !streaming && tc.wantNote != "" {
					want = md + "\n`" + tc.wantNote + "`  \n"
				}
				if got != want {
					t.Fatalf("streaming=%v changed source: got %q, want %q", streaming, got, want)
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
