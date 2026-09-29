package codingagent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// .upstream/v0.87.1/packages/coding-agent/test/user-message.test.ts:27
func TestUpstreamUserMessageTransformerContext(t *testing.T) {
	t.Run("chains Markdown transformers with user message context", func(t *testing.T) {
		var calls []string
		transformers := []extension.MarkdownTransformer{
			func(markdown string, context extension.MarkdownTransformContext) string {
				calls = append(calls, "formula")
				want := extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageUser, IsStreaming: false, AvailableWidth: 78}
				if context != want {
					t.Fatalf("context=%+v, want %+v", context, want)
				}
				return strings.ReplaceAll(markdown, "$x^2$", "x²")
			},
			func(markdown string, _ extension.MarkdownTransformContext) string {
				calls = append(calls, "suffix")
				return markdown + " Done."
			},
		}
		component := tui.NewUserMessageBlock("The input is $x^2$.")
		component.SetMarkdownTransform(createMarkdownTransform(extension.MarkdownMessageUser, false, transformers))
		rendered := widthx.StripAnsi(strings.Join(component.Render(80), "\n"))
		if !strings.Contains(rendered, "The input is x². Done.") {
			t.Fatalf("transforms not rendered: %q", rendered)
		}
		if !slices.Equal(calls, []string{"formula", "suffix"}) {
			t.Fatalf("calls=%q", calls)
		}
	})
}

// Pi interactive-mode.ts:2137-2138,3801-3814 passes the built-in transformer to user messages as well as assistant messages.
func TestInteractiveUserMessageRunsBuiltinMarkdownTransform(t *testing.T) {
	previousTheme, previousCaps := tui.ActiveTheme().Name, tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps); tui.SetThemeByName(previousTheme) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	m := &InteractiveMode{outputPad: 1}
	block := m.newUserMessageBlock("```mermaid\nflowchart LR\nA --> B\n```")
	plain := widthx.StripAnsi(strings.Join(block.Render(80), "\n"))
	if strings.Contains(plain, "```mermaid") || !strings.Contains(plain, "┌") || !strings.Contains(plain, "A") || !strings.Contains(plain, "B") {
		t.Fatalf("user Mermaid transform missing: %q", plain)
	}
	data, err := json.Marshal(struct {
		Name  string   `json:"name"`
		Lines []string `json:"lines"`
	}{"user-builtin-transform", block.Render(80)})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("USER_BUILTIN_TRANSFORM:%s", data)
}
