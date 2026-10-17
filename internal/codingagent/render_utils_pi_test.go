package codingagent

// pi: packages/coding-agent/src/core/tools/render-utils.ts

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TestRenderUtilsReplaceTabsAndTextOutputMatchPi pins replaceTabs (render-utils.ts:31-33) and the getTextOutput
// (render-utils.ts:39-64) behind the built-in shell, read and list renderers. The expectations were produced by the Pi 1.1.0
// render-utils.js with images unsupported: each text block is stripped of ANSI, sanitized and loses every carriage
// return, and an image block without a MIME type is shown as image/unknown.
func TestRenderUtilsReplaceTabsAndTextOutputMatchPi(t *testing.T) {
	if got, want := replaceTabs("\ta\t\tb"), "   a      b"; got != want {
		t.Errorf("replaceTabs = %q, want %q", got, want)
	}
	if got, want := shellTextOutput("one\r\ntwo\r\x1b[31mred\x1b[39m\x07\uFFF9!"), "one\ntwored!"; got != want {
		t.Errorf("shellTextOutput = %q, want %q", got, want)
	}

	previous := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previous) })
	tui.SetCapabilities(tui.TerminalCapabilities{})
	for _, tc := range []struct {
		name       string
		content    []ai.ToolResultMessageContent
		showImages bool
		want       string
	}{
		{"text blocks", []ai.ToolResultMessageContent{ai.TextContent{Text: "one\r\ntwo\r\x1b[31mred\x1b[39m\x07\uFFF9!"}, ai.TextContent{Text: "\r"}}, true, "one\ntwored!\n"},
		{"image without a MIME type", []ai.ToolResultMessageContent{ai.TextContent{Text: "out"}, ai.ImageContent{Data: "abc"}}, true, "out\n[Image: [image/unknown]]"},
		{"hidden image alone", []ai.ToolResultMessageContent{ai.ImageContent{Data: "abc", MimeType: "image/png"}}, false, "[Image: [image/png]]"},
	} {
		if got := toolResultTextOutput(agent.AgentToolResult{Content: tc.content}, tc.showImages); got != tc.want {
			t.Errorf("%s: toolResultTextOutput = %q, want %q", tc.name, got, tc.want)
		}
	}

	// render-utils.ts:51: a terminal that shows images drops the indicators only while images are shown.
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty})
	image := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "out"}, ai.ImageContent{Data: "abc", MimeType: "image/png"}}}
	if got, want := toolResultTextOutput(image, true), "out"; got != want {
		t.Errorf("shown image: toolResultTextOutput = %q, want %q", got, want)
	}
	if got, want := toolResultTextOutput(image, false), "out\n[Image: [image/png]]"; got != want {
		t.Errorf("hidden image: toolResultTextOutput = %q, want %q", got, want)
	}

	// Caller path: the expanded ls card renders getTextOutput's image/unknown indicator.
	tui.SetCapabilities(tui.TerminalCapabilities{})
	card := listRenderResult("ls")(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "a.go"}, ai.ImageContent{Data: "abc"}}},
		extension.ToolRenderResultOptions{Expanded: true}, nil, extension.ToolRenderContext{Expanded: true})
	if rendered := widthx.StripAnsi(strings.Join(card.Render(80), "\n")); !strings.Contains(rendered, "a.go") || !strings.Contains(rendered, "[Image: [image/unknown]]") {
		t.Errorf("ls card = %q, want the text and the [Image: [image/unknown]] indicator", rendered)
	}
}
