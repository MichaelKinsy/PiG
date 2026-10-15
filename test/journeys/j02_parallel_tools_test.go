//go:build parity

package journeys

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

func lines(n int, format string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, format+"\n", i)
	}
	return b.String()
}

func init() {
	register(&harness.Journey{
		Name:    "parallel-tools",
		Doc:     "Parallel read/bash/grep calls in one assistant turn, edit and write tool diffs, tool errors, truncated output and tool-output expansion.",
		Touches: []string{"coding/tools", "agent/", "tui/", "coding/", "internal/codingagent/"},
		Env: func(harness.Kind) harness.Env {
			return harness.Env{Files: map[string]string{
				"notes.txt":   "alpha\nbeta\ngamma\ndelta\n",
				"src/main.go": "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n",
				"data.csv":    lines(40, "row-%d,value-%d"),
			}}
		},
		Script: script(
			onPrompt("PAR", fixed(harness.Say("Reading three things at once.").Then(harness.Use(
				harness.Call{Name: "read", Args: map[string]any{"path": "notes.txt"}},
				harness.Call{Name: "bash", Args: map[string]any{"command": harness.GateWait("a") + "; echo A-done"}},
				harness.Call{Name: "bash", Args: map[string]any{"command": harness.GateWait("b") + "; echo B-done"}},
			)))),
			afterTools("PAR", func(r *harness.Request, res []harness.Message) harness.Reply {
				return harness.Say(fmt.Sprintf("Done: %d results arrived in order.", len(res)))
			}),
			onPrompt("EDIT", fixed(harness.Use(harness.Call{Name: "edit", Args: map[string]any{
				"path":  "src/main.go",
				"edits": []any{map[string]any{"oldText": "println(\"hello\")", "newText": "println(\"hello, journey\")\n\tprintln(\"second line\")"}},
			}}))),
			afterToolsFixed("EDIT", harness.Say("Edited main.go.")),
			onPrompt("WRITE", fixed(harness.Use(harness.Call{Name: "write", Args: map[string]any{"path": "out/report.md", "content": "# Report\n\nline one\nline two\n"}}))),
			afterToolsFixed("WRITE", harness.Say("Wrote the report.")),
			onPrompt("MISSING", fixed(harness.Use(
				harness.Call{Name: "read", Args: map[string]any{"path": "does-not-exist.txt"}},
				harness.Call{Name: "bash", Args: map[string]any{"command": "echo partial-ok; exit 3"}},
			))),
			afterToolsFixed("MISSING", harness.Say("Both tools failed as expected.")),
			onPrompt("BIG", fixed(harness.Use(harness.Call{Name: "bash", Args: map[string]any{"command": "seq 1 3000"}}))),
			afterToolsFixed("BIG", harness.Say("Output was truncated.")),
			onPrompt("CSV", fixed(harness.Use(harness.Call{Name: "read", Args: map[string]any{"path": "data.csv", "offset": 5, "limit": 8}}))),
			afterToolsFixed("CSV", harness.Say("Read a window of the file.")),
		),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "parallel", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("PAR three tools")
				c.WaitFor("echo A-done", "echo B-done")
				c.Quiet(c.Settle())
				c.Snap("tools-running")
				// Finish the tools in a harness-chosen order so completion order is the same for both products.
				c.Open("b")
				c.WaitFor("B-done")
				c.Snap("b-finished")
				c.Open("a")
				c.Idle(base + 2)
				c.WaitFor("Done: 3 results")
				c.Snap("done")
			}}},
			{Step: harness.Step{Name: "edit", Run: turn("EDIT main.go", 2, "Edited main.go.")}},
			{Step: harness.Step{Name: "write", Run: turn("WRITE the report", 2, "Wrote the report.")}},
			{Step: harness.Step{Name: "errors", Run: turn("MISSING files", 2, "Both tools failed")}},
			{Step: harness.Step{Name: "truncated", Run: turn("BIG output", 2, "Output was truncated.")}},
			{Step: harness.Step{Name: "read-window", Run: turn("CSV window", 2, "Read a window")}},
			{Step: harness.Step{Name: "expand", Run: func(c *harness.Ctx) {
				c.Key("C-o")
				c.Quiet(120e6)
				c.Snap("expanded")
				c.Key("C-o")
				c.Quiet(120e6)
				c.Snap("collapsed")
			}}, Needs: []string{"parallel"}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}

// turn returns a step that submits prompt, waits for requests extra provider requests (the prompt's own plus tool follow-ups), waits for final text and records a frame.
func turn(prompt string, requests int, final string) func(c *harness.Ctx) {
	return func(c *harness.Ctx) {
		base := len(c.P.Requests())
		c.Say(prompt)
		c.Idle(base + requests)
		c.WaitFor(final)
		c.Snap("done")
	}
}
