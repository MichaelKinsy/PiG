// pi's Mermaid Markdown transformer in p2 (p2-tui-port.md P12): modes from the visitor's `markdown.mermaid` setting, the instance theme, and the
// fallbacks (too wide, invalid, thinking).
//
// pi: apps/p2/test/p2/tui-mermaid.test.ts
package components

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

const mermaidDiagram = "Before\n\n```mermaid\ngraph TD\nA-->B\n```\n\nAfter\n"

func mermaidContext(t *testing.T, patch func(*render.MarkdownTransformContext)) render.MarkdownTransformContext {
	t.Helper()
	dark, _ := theme.BuiltinTheme("dark")
	th, err := theme.CreateTheme(dark, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	context := render.MarkdownTransformContext{MessageType: render.MessageAssistant, AvailableWidth: 80, Theme: th}
	if patch != nil {
		patch(&context)
	}
	return context
}

func mermaidSettings(mode string) *prefs.TuiSettings {
	return &prefs.TuiSettings{Markdown: &prefs.MarkdownSettings{Mermaid: &mode}}
}

func TestMermaidRendersTopLevelBlocksByDefault(t *testing.T) {
	out := MermaidMarkdownTransformer(mermaidDiagram, mermaidContext(t, nil))
	if strings.Contains(out, "```mermaid") {
		t.Fatalf("source kept: %q", out)
	}
	if !regexp.MustCompile(`│ A │[\s\S]*▼[\s\S]*│ B │`).MatchString(widthx.StripAnsi(out)) {
		t.Fatalf("no diagram: %q", out)
	}
	if !strings.HasPrefix(out, "Before") || !strings.HasSuffix(out, "After\n") {
		t.Fatalf("surroundings: %q", out)
	}
	if out == widthx.StripAnsi(out) {
		t.Fatal("not themed")
	}
	streaming := MermaidMarkdownTransformer(mermaidDiagram, mermaidContext(t, func(c *render.MarkdownTransformContext) { c.IsStreaming = true }))
	if !strings.Contains(widthx.StripAnsi(streaming), "│ A │") {
		t.Fatalf("streaming: %q", streaming)
	}
}

func TestMermaidModes(t *testing.T) {
	with := func(mode string, streaming bool) string {
		return MermaidMarkdownTransformer(mermaidDiagram, mermaidContext(t, func(c *render.MarkdownTransformContext) {
			c.Settings, c.IsStreaming = mermaidSettings(mode), streaming
		}))
	}
	if with("off", false) != mermaidDiagram {
		t.Fatal("off rendered")
	}
	if with("final", true) != mermaidDiagram {
		t.Fatal("final rendered while streaming")
	}
	if !strings.Contains(widthx.StripAnsi(with("final", false)), "│ A │") {
		t.Fatal("final did not render")
	}
	thinking := MermaidMarkdownTransformer(mermaidDiagram, mermaidContext(t, func(c *render.MarkdownTransformContext) { c.MessageType = render.MessageAssistantThinking }))
	if thinking != mermaidDiagram {
		t.Fatal("thinking rendered")
	}
}

func TestMermaidFallbacks(t *testing.T) {
	narrow := MermaidMarkdownTransformer(mermaidDiagram, mermaidContext(t, func(c *render.MarkdownTransformContext) { c.AvailableWidth = 4 }))
	if narrow != mermaidDiagram {
		t.Fatalf("too wide: %q", narrow)
	}
	invalid := "```mermaid\ngraph TD\nA-->\n```\n"
	out := MermaidMarkdownTransformer(invalid, mermaidContext(t, nil))
	if !strings.Contains(out, "```mermaid\ngraph TD\nA-->\n```") {
		t.Fatalf("source dropped: %q", out)
	}
	if !strings.Contains(widthx.StripAnsi(out), "Mermaid diagram not rendered: dropped, link has no target") {
		t.Fatalf("no reason: %q", out)
	}
	streaming := MermaidMarkdownTransformer(invalid, mermaidContext(t, func(c *render.MarkdownTransformContext) { c.IsStreaming = true }))
	if strings.Contains(streaming, "not rendered") {
		t.Fatalf("reported while streaming: %q", streaming)
	}
}
