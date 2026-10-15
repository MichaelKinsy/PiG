//go:build parity

package journeys

import (
	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

func init() {
	register(&harness.Journey{
		Name:    "thinking-levels",
		Doc:     "A reasoning model: streamed reasoning blocks, hiding and showing them with Ctrl+T, cycling the thinking level with Shift+Tab (status line, footer, thinking_level_change entries) and the reasoning effort each level puts on the request.",
		Touches: []string{"internal/codingagent/", "ai/", "agent/", "tui/", "coding/"},
		Env:     func(harness.Kind) harness.Env { return harness.Env{Reasoning: true} },
		Script: script(
			onPrompt("THINK", fixed(harness.Think("First I consider the question. ").Then(harness.Think("Then I weigh two options.")).Then(harness.Say("Answer after thinking.")))),
		),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "reasoning-turn", Run: func(c *harness.Ctx) {
				prompt(c, "THINK about it", 1)
				c.WaitFor("Answer after thinking.")
				c.Quiet(c.Settle())
				c.Snap("reasoning-shown")
			}}},
			{Needs: []string{"reasoning-turn"}, Step: harness.Step{Name: "toggle-thinking-blocks", Run: func(c *harness.Ctx) {
				c.Key("C-t")
				c.WaitFor("Thinking blocks: hidden")
				c.Quiet(c.Settle())
				c.Snap("thinking-hidden")
				c.Key("C-t")
				c.WaitFor("Thinking blocks: visible")
				c.Quiet(c.Settle())
				c.Snap("thinking-visible")
			}}},
			{Step: harness.Step{Name: "cycle-level", Run: func(c *harness.Ctx) {
				for range 3 {
					c.Key("BTab")
					c.Quiet(c.Settle())
					c.Quiet(c.Settle())
					c.Snap("level-cycled")
				}
				prompt(c, "THINK at the cycled level", 1)
				c.WaitFor("Answer after thinking.")
				c.Quiet(c.Settle())
				c.Snap("answered-at-level")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
