//go:build parity

package journeys

import (
	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

func init() {
	register(&harness.Journey{
		Name:    "bash-commands-escape",
		Doc:     "The ! and !! user commands: output, failure status, a command excluded from the model context, a second command while one runs, Escape on a running command, truncated output, and a prompt that must carry the recorded executions to the model (interactive-mode.ts handleBashCommand).",
		Touches: []string{"internal/codingagent/", "coding/tools", "coding/", "tui/"},
		Script:  script(),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "bash-output", Run: func(c *harness.Ctx) {
				c.Say("!printf 'out-%s\\n' one")
				c.WaitFor("out-one")
				c.Quiet(c.Settle())
				c.Snap("output")
			}}},
			{Step: harness.Step{Name: "bash-failure", Run: func(c *harness.Ctx) {
				c.Say("!sh -c 'echo to-stderr-%s >&2; exit 3' x")
				c.WaitFor("(exit 3)")
				c.Quiet(c.Settle())
				c.Snap("failed")
			}}},
			{Step: harness.Step{Name: "bash-excluded", Run: func(c *harness.Ctx) {
				c.Say("!!printf 'hidden-%s\\n' two")
				c.WaitFor("hidden-two")
				c.Quiet(c.Settle())
				c.Snap("excluded")
			}}},
			{Step: harness.Step{Name: "bash-escape", Run: func(c *harness.Ctx) {
				c.Say("!" + harness.GateWait("esc-cmd") + "; echo unreachable-esc")
				c.WaitFor("unreachable-esc")
				c.Quiet(c.Settle())
				c.Snap("running")
				c.Key("Escape")
				c.WaitFor("(cancelled)")
				c.Quiet(c.Settle())
				c.Snap("cancelled")
			}}},
			{Step: harness.Step{Name: "bash-while-running", Run: func(c *harness.Ctx) {
				c.Say("!" + harness.GateWait("busy-cmd") + "; echo finished-busy")
				c.WaitFor("finished-busy")
				c.Quiet(c.Settle())
				c.Snap("first-running")
				c.Say("!echo second-never-runs")
				c.Quiet(c.Settle())
				c.Snap("second-refused")
				c.Key("C-u") // the refused command stays in the editor
				c.Quiet(c.Settle())
				c.Open("busy-cmd")
				c.WaitGone("Running")
				c.Quiet(c.Settle())
				c.Snap("first-done")
			}}},
			{Step: harness.Step{Name: "bash-truncated", Run: func(c *harness.Ctx) {
				c.Say("!seq 1 3000")
				c.WaitFor("3000")
				c.Quiet(c.Settle())
				c.Snap("truncated")
				c.Key("C-o")
				c.Quiet(c.Settle())
				c.Snap("truncated-expanded")
				c.Key("C-o")
				c.Quiet(c.Settle())
			}}},
			{Step: harness.Step{Name: "prompt-after-bash", Run: func(c *harness.Ctx) {
				prompt(c, "What did the commands print?", 1)
				c.WaitFor("Echo: What did the commands print?")
				c.Snap("prompted")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
