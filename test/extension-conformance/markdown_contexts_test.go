package extensionconformance

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi markdown-transform.ts:18-29 passes the current display context to each callback. Use the existing common fixture and compare all SDKs and placements to the real native reference without polling away a missing first answer.
func TestMarkdownContextsAcrossSDKs(t *testing.T) {
	t.Parallel()
	cases := allHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			transforms := h.runner.GetMarkdownTransformers()
			if len(transforms) == 0 {
				t.Fatal("common fixture has no Markdown transformer")
			}
			for _, text := range []string{"", "ordinary *Markdown*", "猫😀\nsecond line", strings.Repeat("x", 64<<10)} {
				for i, messageType := range []extension.MarkdownMessageType{extension.MarkdownMessageUser, extension.MarkdownMessageAssistant, extension.MarkdownMessageAssistantThinking} {
					ctx := extension.MarkdownTransformContext{Context: t.Context(), MessageType: messageType, IsStreaming: i == 1, AvailableWidth: []int{1, 17, 4096}[i]}
					want := fmt.Sprintf("md:%s:%s:streaming=%t:width=%d", text, messageType, ctx.IsStreaming, ctx.AvailableWidth)
					for j, transform := range transforms {
						if got := transform(text, ctx); got != want {
							t.Fatalf("transform%d input bytes=%d context=%+v: got %q; want %q", j, len(text), ctx, got, want)
						}
					}
				}
			}
		})
	}
}
