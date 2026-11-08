//go:build !pig_strip_mermaid

package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/mermaid.ts

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
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:31
	t.Run("replaces Mermaid code blocks with Unicode diagrams", func(t *testing.T) {
		out := normal("Before\n\n```mermaid\nflowchart LR\n  A[Start] --> B[Done]\n```\nAfter")
		assertToolContains(t, out, "Before", "┌───────┐", "│ Start ├───▶│ Done │", "└───────┘    └──────┘`\nAfter", "After")
		lacks(t, out, "```mermaid")
	})
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:43
	t.Run("leaves unsupported and oversized diagrams unchanged", func(t *testing.T) {
		unsupported := "```mermaid\ngantt\n  title Plan\n```"
		if got := normal(unsupported); got != unsupported {
			t.Errorf("unsupported=%q, want %q", got, unsupported)
		}
		oversized := "```mermaid\nflowchart LR\n  A[Start] --> B[Done]\n```"
		if got := transform(oversized, "streaming", false, 10, extension.MarkdownMessageAssistant, nil); got != oversized {
			t.Errorf("oversized=%q, want %q", got, oversized)
		}
	})
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:51
	t.Run("renders diagram types added by lovely-mermaid", func(t *testing.T) {
		out := normal("```mermaid\npie\n  title Pets\n  \"Dogs\" : 4\n```")
		assertToolContains(t, out, "Pets", "Dogs", "████████████████████")
		lacks(t, out, "```mermaid")
	})
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:60
	t.Run("maps semantic spans through the Pi theme", func(t *testing.T) {
		// The native theme is concrete rather than a callback object. Assert the SGR parameters of its border and edge foreground tokens at the same span boundaries: each is the theme's own escape without its ESC[ and final m.
		theme := tui.ActiveTheme()
		sgr := func(token string) string { a := theme.GetFgAnsi(token); return a[2 : len(a)-1] }
		border, accent := "\x1b["+sgr("borderMuted")+"m", "\x1b["+sgr("accent")+"m"
		if border == accent {
			t.Fatalf("theme fixture needs distinct role markers: border=%q accent=%q", border, accent)
		}
		out := transform("```mermaid\nflowchart LR\n  A --> B\n```", "streaming", false, 100, extension.MarkdownMessageAssistant, theme)
		assertToolContains(t, out, border, accent)
	})
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:79
	t.Run("applies author class styles over the Pi theme", func(t *testing.T) {
		out := transform("```mermaid\nflowchart LR\n  A[Hot]:::highlight --> B[Plain]\n  classDef highlight fill:#f96,stroke:#333,color:#000,font-weight:bold\n```", "streaming", false, 100, extension.MarkdownMessageAssistant, tui.ActiveTheme())
		assertToolContains(t, out, "\x1b[1;38;2;51;51;51;48;2;255;153;102m", "\x1b[1;38;2;0;0;0;48;2;255;153;102m")
		lacks(t, out, "```mermaid")
	})
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:94
	t.Run("links only Mermaid labels and only when the terminal supports hyperlinks", func(t *testing.T) {
		t.Cleanup(tui.ResetCapabilitiesCache)
		md := "```mermaid\nflowchart TD\n  A[Docs] --> B[Done]\n  click A \"https://example.com/docs\"\n```"
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true, Hyperlinks: true})
		linked := transform(md, "streaming", false, 100, extension.MarkdownMessageAssistant, tui.ActiveTheme())
		if n := len(strings.Split(linked, "\x1b]8;;https://example.com/docs\x1b\\")); n != 2 {
			t.Fatalf("link opened %d times, want once: %q", n-1, linked)
		}
		lacks(t, linked, "```mermaid")
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true, Hyperlinks: false})
		unlinked := transform(md, "streaming", false, 100, extension.MarkdownMessageAssistant, tui.ActiveTheme())
		lacks(t, unlinked, "\x1b]8;;")
		lacks(t, unlinked, "https://example.com/docs")
		lacks(t, unlinked, "```mermaid")
	})
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:114
	t.Run("renders incomplete Mermaid blocks during streaming", func(t *testing.T) {
		assertToolContains(t, transform("```mermaid\nflowchart LR\n  A --> B", "streaming", true, 100, extension.MarkdownMessageAssistant, nil), "───▶")
	})
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:120
	t.Run("renders diagrams with class assignments", func(t *testing.T) {
		out := normal("```mermaid\nflowchart LR\n  A[Foo]:::highlight --> B[Bar]\n```")
		lacks(t, out, "```mermaid")
		lacks(t, out, "Mermaid diagram not rendered")
		assertToolContains(t, out, "│ Foo ├───▶│ Bar │")
	})
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:129
	t.Run("falls back to the code block with a warning after streaming", func(t *testing.T) {
		md := "```mermaid\nflowchart LR\n  A --> B\n  broken !\n```"
		out := normal(md)
		assertToolContains(t, out, md, "```\n`Mermaid diagram not rendered", `dropped, expected a link: "!"`)
		lacks(t, out, "more)")
		assertToolContains(t, normal(md+"\nFollowing text"), "  \nFollowing text")
		stream := transform(md, "streaming", true, 100, extension.MarkdownMessageAssistant, nil)
		lacks(t, stream, "Mermaid diagram not rendered")
		lacks(t, stream, "```mermaid")
		assertToolContains(t, stream, "│ broken │")
	})
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:145
	t.Run("summarizes additional partial-render warnings", func(t *testing.T) {
		md := "```mermaid\nflowchart LR\n  A --> B\n  broken !\n  other ?\n```"
		out := normal(md)
		assertToolContains(t, out, md, `dropped, expected a link: "!"`, "(+1 more)")
		lacks(t, out, `dropped, expected a link: "?"`)
	})
	// .upstream/current/packages/coding-agent/test/mermaid.test.ts:155
	t.Run("respects rendering modes and skips thinking blocks and non-transcript surfaces", func(t *testing.T) {
		md := "```mermaid\nflowchart LR\n  A --> B\n```"
		for _, tc := range []struct {
			mode      string
			streaming bool
			kind      extension.MarkdownMessageType
		}{
			{"off", false, extension.MarkdownMessageAssistant},
			{"final", true, extension.MarkdownMessageAssistant},
			{"streaming", false, extension.MarkdownMessageAssistantThinking},
			{"final", false, extension.MarkdownMessageType("custom")},
			{"final", false, extension.MarkdownMessageType("changelog")},
		} {
			if out := transform(md, tc.mode, tc.streaming, 100, tc.kind, nil); out != md {
				t.Fatalf("%+v changed source: %q", tc, out)
			}
		}
		lacks(t, transform(md, "final", false, 100, extension.MarkdownMessageAssistant, nil), "```mermaid")
		lacks(t, transform(md, "final", false, 100, extension.MarkdownMessageUser, nil), "```mermaid")
		lacks(t, transform(md, "final", false, 100, extension.MarkdownMessageType("summary"), nil), "```mermaid")
	})
}
