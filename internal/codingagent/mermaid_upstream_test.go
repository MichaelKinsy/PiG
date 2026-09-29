package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestMermaidRenderingUpstream(t *testing.T) {
	assertToolContains := func(t *testing.T, text string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q: %q", want, text)
			}
		}
	}
	transform := func(md, mode string, streaming bool, width int, kind extension.MarkdownMessageType, theme *tui.Theme) string {
		return createMermaidMarkdownTransformer(func() string { return mode }, theme)(md, extension.MarkdownTransformContext{AvailableWidth: width, IsStreaming: streaming, MessageType: kind})
	}
	normal := func(md string) string {
		return transform(md, "streaming", false, 100, extension.MarkdownMessageAssistant, nil)
	}
	lacks := func(t *testing.T, text, part string) {
		t.Helper()
		if strings.Contains(text, part) {
			t.Fatalf("unexpected %q in %q", part, text)
		}
	}
	// .upstream/v0.87.1/packages/coding-agent/test/mermaid.test.ts:28
	t.Run("replaces Mermaid code blocks with Unicode diagrams", func(t *testing.T) {
		out := normal("Before\n\n```mermaid\nflowchart LR\n  A[Start] --> B[Done]\n```\nAfter")
		assertToolContains(t, out, "Before", "┌───────┐", "│ Start ├───▶│ Done │", "└───────┘    └──────┘`\nAfter", "After")
		lacks(t, out, "```mermaid")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/mermaid.test.ts:40
	t.Run("leaves unsupported and oversized diagrams unchanged", func(t *testing.T) {
		unsupported := "```mermaid\npie\n  title Pets\n  \"Dogs\" : 4\n```"
		if got := normal(unsupported); got != unsupported {
			t.Errorf("unsupported=%q, want %q", got, unsupported)
		}
		oversized := "```mermaid\nflowchart LR\n  A[Start] --> B[Done]\n```"
		if got := transform(oversized, "streaming", false, 10, extension.MarkdownMessageAssistant, nil); got != oversized {
			t.Errorf("oversized=%q, want %q", got, oversized)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/mermaid.test.ts:48
	t.Run("maps semantic spans through the Pi theme", func(t *testing.T) {
		// The native theme is concrete rather than a callback object. Assert its distinct border and edge foreground tokens at the same span boundary.
		theme := tui.ActiveTheme()
		border, accent := theme.Fg("borderMuted"), theme.Fg("accent")
		if border == "" || accent == "" || border == accent {
			t.Fatalf("theme fixture needs distinct nonempty role markers: border=%q accent=%q", border, accent)
		}
		out := transform("```mermaid\nflowchart LR\n  A --> B\n```", "streaming", false, 100, extension.MarkdownMessageAssistant, theme)
		assertToolContains(t, out, border, accent)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/mermaid.test.ts:59
	t.Run("renders incomplete Mermaid blocks during streaming", func(t *testing.T) {
		assertToolContains(t, transform("```mermaid\nflowchart LR\n  A --> B", "streaming", true, 100, extension.MarkdownMessageAssistant, nil), "───▶")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/mermaid.test.ts:65
	t.Run("renders diagrams with class assignments", func(t *testing.T) {
		out := normal("```mermaid\nflowchart LR\n  A[Foo]:::highlight --> B[Bar]\n```")
		lacks(t, out, "```mermaid")
		lacks(t, out, "Mermaid diagram not rendered")
		assertToolContains(t, out, "│ Foo ├───▶│ Bar │")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/mermaid.test.ts:74
	t.Run("falls back to the code block with a warning after streaming", func(t *testing.T) {
		md := "```mermaid\nflowchart LR\n  A[Foo] invalid\n```"
		out := normal(md)
		assertToolContains(t, out, md, "```\n`Mermaid diagram not rendered", `dropped, expected a link: "invalid"`)
		lacks(t, out, "more)")
		assertToolContains(t, normal(md+"\nFollowing text"), "  \nFollowing text")
		stream := transform(md, "streaming", true, 100, extension.MarkdownMessageAssistant, nil)
		lacks(t, stream, "Mermaid diagram not rendered")
		lacks(t, stream, "```mermaid")
		assertToolContains(t, stream, "│ Foo │")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/mermaid.test.ts:90
	t.Run("summarizes additional partial-render warnings", func(t *testing.T) {
		md := "```mermaid\nflowchart LR\n  A[Foo] invalid\n  B[Bar] also-invalid\n```"
		out := normal(md)
		assertToolContains(t, out, md, `dropped, expected a link: "invalid"`, "(+1 more)")
		lacks(t, out, `dropped, expected a link: "also-invalid"`)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/mermaid.test.ts:100
	t.Run("respects rendering modes and skips thinking blocks", func(t *testing.T) {
		md := "```mermaid\nflowchart LR\n  A --> B\n```"
		for _, tc := range []struct {
			mode      string
			streaming bool
			kind      extension.MarkdownMessageType
		}{{"off", false, extension.MarkdownMessageAssistant}, {"final", true, extension.MarkdownMessageAssistant}, {"streaming", false, extension.MarkdownMessageAssistantThinking}} {
			if out := transform(md, tc.mode, tc.streaming, 100, tc.kind, nil); out != md {
				t.Fatalf("%+v changed source: %q", tc, out)
			}
		}
		lacks(t, transform(md, "final", false, 100, extension.MarkdownMessageAssistant, nil), "```mermaid")
	})
}
