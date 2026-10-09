package codingagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"
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
		component := tui.NewUserMessageComponent("The input is $x^2$.", nil, 1, nil)
		component.SetMarkdownTransform(markdowntransform.CreateMarkdownTransform(extension.MarkdownMessageUser, false, transformers))
		rendered := widthx.StripAnsi(strings.Join(component.Render(80), "\n"))
		if !strings.Contains(rendered, "The input is x². Done.") {
			t.Fatalf("transforms not rendered: %q", rendered)
		}
		if !slices.Equal(calls, []string{"formula", "suffix"}) {
			t.Fatalf("calls=%q", calls)
		}
	})
}
