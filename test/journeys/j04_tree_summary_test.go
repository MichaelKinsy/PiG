//go:build parity

package journeys

import (
	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

const branchSummary = "## Goal\nSummary of the abandoned branch.\n\n## Progress\n- explored the other path"

func init() {
	register(&harness.Journey{
		Name:        "tree-navigate-branch-summary",
		Normalizers: []harness.Normalizer{estimatedFooterPercent},
		Doc:         "Build a conversation with a tool turn, navigate /tree to earlier nodes with no summary, a generated summary and a custom-prompt summary, label and search nodes, and send follow-ups: no navigation may leave an orphan tool result (the HTTP 400 class) in a later provider request.",
		Touches:     []string{"coding/", "internal/codingagent/", "agent/", "tui/", "coding/compaction"},
		Env: func(harness.Kind) harness.Env {
			return harness.Env{Files: map[string]string{"facts.txt": "the capital is Paris\nthe river is the Seine\n"}}
		},
		Script: script(
			onSummary(fixed(harness.Say(branchSummary))),
			onPrompt("TOOLTURN", fixed(harness.Say("Let me look at the file.").Then(harness.Use(harness.Call{Name: "read", Args: map[string]any{"path": "facts.txt"}})))),
			afterToolsFixed("TOOLTURN", harness.Say("The file names Paris and the Seine.")),
		),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
			{Name: "history", Run: func(c *harness.Ctx) {
				prompt(c, "Topic one: rivers", 1)
				prompt(c, "TOOLTURN read the facts", 2)
				prompt(c, "Topic three: mountains", 1)
				prompt(c, "Topic four: deserts", 1)
				c.Snap("history")
			}},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "tree-open-close", Run: func(c *harness.Ctx) {
				c.Say("/tree")
				c.WaitFor("Type to search:")
				c.Quiet(c.Settle())
				c.Snap("tree")
				c.Key("Up")
				c.Key("Up")
				c.Quiet(c.Settle())
				c.Snap("tree-moved")
				c.Esc()
				c.WaitGone("Type to search:")
				c.Snap("tree-closed")
			}}},
			{Step: harness.Step{Name: "navigate-no-summary", Run: func(c *harness.Ctx) {
				c.Say("/tree")
				c.WaitFor("Type to search:")
				for range 2 + c.Intn(4) {
					c.Key("Up")
				}
				c.Quiet(c.Settle())
				c.Snap("tree-target")
				c.Key("Enter")
				c.WaitFor("Summarize branch?")
				c.Snap("summarize-prompt")
				c.Key("Enter") // "No summary"
				c.WaitGone("Summarize branch?")
				c.Quiet(c.Settle())
				c.Snap("navigated")
				prompt(c, "Follow-up after navigating without a summary", 1)
				c.Snap("followup")
			}}},
			{Step: harness.Step{Name: "navigate-summarize", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("/tree")
				c.WaitFor("Type to search:")
				for range 3 + c.Intn(3) {
					c.Key("Up")
				}
				c.Key("Enter")
				c.WaitFor("Summarize branch?")
				c.Key("Down")
				c.Key("Enter") // "Summarize"
				c.Idle(base + 1)
				c.Quiet(c.Settle())
				c.Snap("summarized")
				prompt(c, "Follow-up after a generated branch summary", 1)
				c.Snap("followup")
			}}},
			{Step: harness.Step{Name: "navigate-custom-summary", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("/tree")
				c.WaitFor("Type to search:")
				for range 2 + c.Intn(3) {
					c.Key("Up")
				}
				c.Key("Enter")
				c.WaitFor("Summarize branch?")
				c.Key("Down")
				c.Key("Down")
				c.Key("Enter") // "Summarize with custom prompt"
				c.Quiet(c.Settle())
				c.Snap("custom-prompt")
				c.Type("focus on the rivers")
				c.Key("Enter")
				c.Idle(base + 1)
				c.Quiet(c.Settle())
				c.Snap("custom-summarized")
				prompt(c, "Follow-up after a custom branch summary", 1)
				c.Snap("followup")
			}}},
			{Step: harness.Step{Name: "tree-search-label", Run: func(c *harness.Ctx) {
				c.Say("/tree")
				c.WaitFor("Type to search:")
				c.Type("rivers")
				c.Quiet(c.Settle())
				c.Snap("search")
				c.Key("S-l")
				c.Quiet(c.Settle())
				c.Snap("label-editor")
				c.Type("river-node")
				c.Key("Enter")
				c.Quiet(c.Settle())
				c.Snap("labelled")
				c.Esc() // clears the search
				c.Snap("search-cleared")
				c.Esc() // closes the tree
				c.WaitGone("Type to search:")
				c.Snap("closed")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
