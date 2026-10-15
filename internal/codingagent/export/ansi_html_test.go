package export

// pi: packages/coding-agent/src/core/export-html/ansi-to-html.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Probed against Pi 0.87.1 core/export-html/ansi-to-html.ts:56-72,205-257.
func TestAnsiToHTMLPiEscapingAndPalette(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"\x1b[31m\"'<&>\x1b[0m", `<span style="color:#800000">&quot;&#039;&lt;&amp;&gt;</span>`},
		{"\x1b[38;5;257mx", `<span style="color:#102102102">x</span>`},
	} {
		if got := ansiToHTML(tc.input); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.input, got, tc.want)
		}
	}
}

// Pi tool-renderer.ts:44-55 strips SGR only and uses JavaScript String.trim's whitespace set.
func TestCustomToolResultHTMLBlankLineSemantics(t *testing.T) {
	for _, tc := range []struct{ name, edge, want string }{
		{"BOM is whitespace", "\ufeff", `<div class="ansi-line">one</div>`},
		{"NEL is content", "\u0085", "<div class=\"ansi-line\">\u0085</div><div class=\"ansi-line\">one</div><div class=\"ansi-line\">\u0085</div>"},
		{"OSC is content", "\x1b]0;title\x07", "<div class=\"ansi-line\">\x1b]0;title\x07</div><div class=\"ansi-line\">one</div><div class=\"ansi-line\">\x1b]0;title\x07</div>"},
		{"SGR whitespace is trimmed", "\x1b[31m \x1b[0m", `<div class="ansi-line">one</div>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := []extension.RegisteredTool{{Definition: extension.ToolDefinition{
				Name: "custom",
				RenderResult: func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
					return testComponent{lines: []string{tc.edge, "one", tc.edge}}
				},
			}}}
			renderer := newToolHTMLRenderer(ToolRenderersOf(tools), "/tmp", 100)
			got := renderer.renderResult("id", "custom", agent.AgentToolResult{})
			if got.ResultHTMLExpanded != tc.want {
				t.Fatalf("got %q, want %q", got.ResultHTMLExpanded, tc.want)
			}
		})
	}
}
