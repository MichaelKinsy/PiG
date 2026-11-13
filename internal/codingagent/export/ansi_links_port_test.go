package export

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/test/export-html-ansi-links.test.ts (Pi 0e1e3fc819b6, #12052).
func osc8Link(url, text, end string) string {
	return "\x1b]8;;" + url + end + text + "\x1b]8;;" + end
}

func TestAnsiToHTMLConvertsOSC8HyperlinksToAnchors(t *testing.T) {
	const st = "\x1b\\"
	if got, want := ansiToHTML("see "+osc8Link("https://example.com/a?b=1&c=2", "\x1b[4mdocs\x1b[24m", st)+"."),
		`see <a href="https://example.com/a?b=1&amp;c=2"><span style="text-decoration:underline">docs</span></a>.`; got != want {
		t.Errorf("https link: got %q, want %q", got, want)
	}
	if got, want := ansiToHTML(osc8Link("mailto:me@example.com", "mail", "\x07")), `<a href="mailto:me@example.com">mail</a>`; got != want {
		t.Errorf("mailto link: got %q, want %q", got, want)
	}
}

func TestAnsiToHTMLKeepsStyledTextNestedInAnchorAndClosesUnterminatedLink(t *testing.T) {
	got := ansiToHTML("\x1b[31mred \x1b]8;;https://example.com\x1b\\link")
	want := `<span style="color:#800000">red </span><a href="https://example.com"><span style="color:#800000">link</span></a>`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAnsiToHTMLDropsUnsafeSchemesAndOtherEscapeSequences(t *testing.T) {
	const st = "\x1b\\"
	for _, tc := range []struct{ name, in, want string }{
		{"javascript", osc8Link("javascript:alert(1)", "x", st), "x"},
		{"control byte in scheme", osc8Link("java\x01script:alert(1)", "x", st), "x"},
		{"other sequences", "\x1b]0;title\x07a\x1b[2Kb\x1b]133;A\x1b\\c\x1b_pi:c\x07d\x1be", "abcde"},
	} {
		if got := ansiToHTML(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Ports the export-html-whitespace.test.ts change of the same commit: a trailing
// reset plus an empty OSC 8 link end is a blank rendered line.
func TestCustomToolResultHTMLTrimsLineWithOnlyEscapeSequences(t *testing.T) {
	tools := []extension.RegisteredTool{{Definition: extension.ToolDefinition{
		Name: "custom", Label: "custom", Description: "custom",
		RenderResult: func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
			return testComponent{lines: []string{"", "\x1b[31mone\x1b[0m", "two", "\x1b[0m\x1b]8;;\x07"}}
		},
	}}}
	renderer := newToolHTMLRenderer(ToolRenderersOf(tools), "/tmp", 100)
	got := renderer.renderResult("id", "custom", agent.AgentToolResult{})
	want := `<div class="ansi-line"><span style="color:#800000">one</span></div><div class="ansi-line">two</div>`
	if got.ResultHTMLExpanded != want || got.ResultHTMLCollapsed != "" {
		t.Fatalf("got %+v, want only expanded %q", got, want)
	}
}

// Pi 0e1e3fc819b6 template.js: clicking a link in an expandable tool output follows the link instead of toggling the section.
func TestExportTemplateExpandableToolOutputIgnoresLinkClicks(t *testing.T) {
	js := mustReadAsset("assets/template.js")
	if !strings.Contains(js, `class="tool-output expandable ansi-rendered" onclick="if(window.getSelection().toString()||event.target.closest('a'))return;`) {
		t.Fatal("ansi-rendered expandable tool output toggles when a link inside it is clicked")
	}
}
