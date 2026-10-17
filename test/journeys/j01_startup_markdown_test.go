//go:build parity

package journeys

import (
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

const markdownReply = "# Release notes\n\nThis reply streams **bold**, _italic_ and `inline code` through the markdown renderer.\n\n" +
	"## Checklist\n\n- first item\n- second item with a [link](https://example.com/docs)\n  - nested item\n1. ordered one\n2. ordered two\n\n" +
	"> A quoted line that should be indented and dimmed.\n\n" +
	"```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n\n" +
	"| Name | Value |\n|------|-------|\n| alpha | 1 |\n| beta | 22 |\n\n" +
	"A closing paragraph that is long enough to wrap across the terminal width so that the wrapping logic of the renderer is exercised by the journey and compared with Pi's output line by line.\n"

func init() {
	register(&harness.Journey{
		Name:    "startup-markdown-quit",
		Doc:     "Cold start, a plain prompt, a streamed markdown reply (headings, lists, quote, code, table, wrapping), two follow-up turns and a clean quit.",
		Touches: []string{"tui/", "coding/", "internal/codingagent/", "cmd/pig/"},
		Env:     func(harness.Kind) harness.Env { return harness.Env{KeepHeader: true} },
		Script: script(
			onPrompt("MD", fixed(harness.Say(markdownReply))),
			onPrompt("STREAM", func(r *harness.Request) harness.Reply {
				return harness.Say("First half of the answer, ").Then(harness.Hold("stream-mid")).Then(harness.Say("second half of the answer."))
			}),
		),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) {
				c.Launch()
				c.Snap("startup")
			}},
			{Name: "first-prompt", Run: func(c *harness.Ctx) {
				c.Say("Hello there, this is the first prompt")
				c.Idle(1)
				c.WaitFor("Echo: Hello there, this is the first prompt")
				c.Snap("after-first-reply")
			}},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "markdown", Run: func(c *harness.Ctx) {
				n := len(c.P.Requests())
				c.Say("MD render everything")
				c.Idle(n + 1)
				c.WaitFor("output line by line.")
				c.Snap("markdown")
			}}},
			{Step: harness.Step{Name: "streaming-hold", Run: func(c *harness.Ctx) {
				n := len(c.P.Requests())
				c.Say("STREAM please")
				c.WaitGate("stream-mid")
				// The provider sends the text in pieces before the hold: wait for its last piece, or the frame catches a product mid-text (seed 30).
				c.WaitFor("First half of the answer,")
				c.Snap("mid-stream")
				c.P.Release("stream-mid")
				c.Idle(n + 1)
				c.Snap("stream-done")
			}}},
			{Step: harness.Step{Name: "long-line-wrap", Run: func(c *harness.Ctx) {
				n := len(c.P.Requests())
				c.Say("Echo this: " + strings.Repeat("wrapping words ", 14))
				c.Idle(n + 1)
				// Idle ends when the provider has answered and the terminal is briefly quiet; a starved product may not have drawn the reply yet (seed 30).
				c.WaitFor("Echo: Echo this: wrapping")
				c.Snap("wrapped")
			}}},
		},
		Tail: []harness.Step{
			{Name: "quit", Run: func(c *harness.Ctx) {
				c.Snap("before-quit")
				c.Quit()
			}},
		},
	})
}
