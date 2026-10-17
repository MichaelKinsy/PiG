//go:build parity

package journeys

import (
	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

func init() {
	register(&harness.Journey{
		Name:    "abort-mid-stream-and-tool",
		Doc:     "Escape during a streaming answer and during a running bash tool, then continue the conversation: the aborted turns must leave a valid transcript, kill the tool's process group, and keep the session usable.",
		Touches: []string{"agent/", "coding/tools", "coding/", "internal/codingagent/", "tui/"},
		Script: script(
			onPrompt("SLOW", fixed(harness.Say("Starting a long answer that will be cut off. ").Then(harness.Hold("slow")).Then(harness.Say("This tail must never be shown.")))),
			onPrompt("TOOLWAIT", fixed(harness.Say("Running a command that never finishes.").Then(harness.Use(
				harness.Call{Name: "bash", Args: map[string]any{"command": harness.GateWait("never-tool") + "; echo unreachable-tool"}})))),
			onPrompt("PARABORT", fixed(harness.Use(
				harness.Call{Name: "bash", Args: map[string]any{"command": "echo quick-one"}},
				harness.Call{Name: "bash", Args: map[string]any{"command": harness.GateWait("never-par") + "; echo unreachable-par"}}))),
		),
		Normalizers: []harness.Normalizer{findingAbortedFetchText, findingAbortedSessionText},
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
			{Name: "warmup", Run: func(c *harness.Ctx) { prompt(c, "Say hello first", 1); c.Snap("warm") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "abort-stream", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("SLOW answer please")
				c.WaitGate("slow")
				c.WaitFor("Starting a long answer")
				c.Quiet(c.Settle())
				c.Snap("streaming")
				c.Key("Escape")
				c.Await("client disconnect", func(string) bool { return c.P.Count(harness.EvDisconnect) >= 1 })
				c.Idle(base + 1)
				c.Snap("aborted")
			}}},
			{Step: harness.Step{Name: "abort-tool", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("TOOLWAIT now")
				c.WaitFor("echo unreachable-tool")
				c.Quiet(c.Settle())
				c.Snap("tool-running")
				c.Key("Escape")
				c.Idle(base + 1)
				c.Snap("tool-aborted")
			}}},
			{Step: harness.Step{Name: "abort-parallel", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("PARABORT both")
				c.WaitFor("quick-one", "echo unreachable-par")
				c.Quiet(c.Settle())
				c.Snap("parallel-running")
				c.Key("Escape")
				c.Idle(base + 1)
				c.Snap("parallel-aborted")
			}}},
			{Step: harness.Step{Name: "continue-after-abort", Run: func(c *harness.Ctx) {
				prompt(c, "Carry on after the abort", 1)
				c.WaitFor("Echo: Carry on after the abort")
				c.Snap("continued")
			}}, Needs: []string{"abort-stream"}},
			{Step: harness.Step{Name: "continue-after-tool-abort", Run: func(c *harness.Ctx) {
				prompt(c, "Carry on after the tool abort", 1)
				c.WaitFor("Echo: Carry on after the tool abort")
				c.Snap("continued")
			}}, Needs: []string{"abort-tool"}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
