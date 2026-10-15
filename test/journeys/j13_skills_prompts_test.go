//go:build parity

package journeys

import (
	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

const reviewPrompt = "---\ndescription: Review a file\nargument-hint: \"<file> <focus>\"\n---\nPlease review $1 and focus on $2. All arguments: $@\n"

const greeterSkill = "---\nname: greeter\ndescription: Greets people in a friendly way\n---\n# Greeter\n\nGreet the user warmly and mention their name.\n"

func init() {
	register(&harness.Journey{
		Name:    "skills-prompts-reload",
		Doc:     "User prompt templates and skills: slash autocomplete listing them, a template expanded with positional arguments, a /skill: command expanded into a skill block, and /reload picking up a template written while the session runs (prompt-templates.ts, skills.ts, interactive-mode.ts handleReloadCommand).",
		Touches: []string{"coding/", "internal/codingagent/", "tui/"},
		Env: func(harness.Kind) harness.Env {
			return harness.Env{AgentDirFiles: map[string]string{
				"prompts/review.md":       reviewPrompt,
				"skills/greeter/SKILL.md": greeterSkill,
			}}
		},
		Script: script(),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "autocomplete-templates", Run: func(c *harness.Ctx) {
				c.Type("/rev")
				c.WaitFor("Review a file")
				c.Quiet(c.Settle())
				c.Snap("template-listed")
				c.Esc()
				c.Key("C-u")
				c.Type("/skill:gre")
				c.WaitFor("Greets people")
				c.Quiet(c.Settle())
				c.Snap("skill-listed")
				// Escape closes the list; Ctrl+U alone leaves it open in both products and a following full command plus Enter in one burst is swallowed by PiG (open finding e2e-3 in the handoff).
				c.Esc()
				c.Key("C-u")
				c.Quiet(c.Settle())
			}}},
			{Step: harness.Step{Name: "run-template", Run: func(c *harness.Ctx) {
				prompt(c, "/review main.go performance", 1)
				c.WaitFor("Echo: Please review main.go")
				c.Quiet(c.Settle())
				c.Snap("template-run")
			}}},
			{Step: harness.Step{Name: "run-skill", Run: func(c *harness.Ctx) {
				prompt(c, "/skill:greeter say hello to Ada", 1)
				c.WaitFor("Echo: <skill")
				c.Quiet(c.Settle())
				c.Snap("skill-run")
			}}},
			{Step: harness.Step{Name: "reload-new-template", Run: func(c *harness.Ctx) {
				c.WriteAgentFile("prompts/summarize.md", "---\ndescription: Summarize a topic\n---\nSummarize $@ in one sentence.\n")
				c.Say("/reload")
				c.WaitFor("Reloaded")
				c.Quiet(c.Settle())
				c.Snap("reloaded")
				prompt(c, "/summarize the journey harness", 1)
				c.WaitFor("Echo: Summarize the journey harness")
				c.Quiet(c.Settle())
				c.Snap("reloaded-template-run")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
