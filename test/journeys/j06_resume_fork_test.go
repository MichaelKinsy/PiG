//go:build parity

package journeys

import (
	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

func init() {
	register(&harness.Journey{
		Name:    "resume-fork-clone",
		Doc:     "Name a session, /fork from an earlier message, /clone, /new, relaunch with --continue and pick another session in /resume: every resumed transcript is rebuilt from the session file and compared frame by frame, and each follow-up request must carry exactly the active path.",
		Touches: []string{"coding/", "internal/codingagent/", "internal/sessionentry/", "tui/"},
		Script:  script(),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
			{Name: "history", Run: func(c *harness.Ctx) {
				prompt(c, "Plan the trip to Lisbon", 1)
				prompt(c, "Add a day in Porto", 1)
				prompt(c, "Pick restaurants", 1)
				c.Say("/name lisbon-plan")
				c.WaitFor("lisbon-plan")
				c.Quiet(c.Settle())
				c.Snap("named")
			}},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "session-info", Run: func(c *harness.Ctx) {
				c.Say("/session")
				c.WaitFor("Session Info")
				c.Quiet(c.Settle())
				c.Snap("session-info")
			}}},
			{Step: harness.Step{Name: "fork", Run: func(c *harness.Ctx) {
				c.Say("/fork")
				c.WaitFor("Fork from Message")
				c.Key("Up") // the second user message
				c.Quiet(c.Settle())
				c.Snap("fork-selector")
				c.Key("Enter")
				c.WaitFor("Forked to new session")
				c.Quiet(c.Settle())
				c.Snap("forked") // the selected message is back in the editor
				c.Key("C-c")     // clears it
				c.Quiet(c.Settle())
				c.Snap("fork-editor-cleared")
				prompt(c, "Continue on the fork", 1)
				c.Say("/name forked-session")
				c.WaitFor("forked-session")
				c.Quiet(c.Settle())
				c.Snap("fork-followup")
			}}},
			{Step: harness.Step{Name: "clone", Run: func(c *harness.Ctx) {
				c.Say("/clone")
				c.WaitFor("Cloned to new session")
				c.Quiet(c.Settle())
				c.Snap("cloned")
				prompt(c, "Continue on the clone", 1)
				c.Say("/name cloned-session")
				c.WaitFor("cloned-session")
				c.Quiet(c.Settle())
				c.Snap("clone-followup")
			}}},
			{Step: harness.Step{Name: "continue-relaunch", Run: func(c *harness.Ctx) {
				c.Restart("--continue")
				c.WaitFor("Echo:")
				c.Quiet(c.Settle())
				c.Snap("resumed")
				prompt(c, "Follow-up after relaunch", 1)
				c.Snap("resumed-followup")
			}}},
			{Step: harness.Step{Name: "resume-selector", Run: func(c *harness.Ctx) {
				c.Say("/resume")
				c.WaitFor("Resume Session")
				c.Quiet(c.Settle())
				c.Snap("resume-selector")
				c.Type("cloned-session") // the one row that matches the name given in the clone action
				c.WaitFor("> cloned-session")
				c.Quiet(c.Settle())
				c.Snap("resume-filtered")
				c.Key("Enter")
				c.WaitFor("Resumed session")
				c.Quiet(c.Settle())
				c.Snap("resumed-other")
				prompt(c, "Follow-up in the other session", 1)
				c.Snap("other-followup")
			}}, Needs: []string{"clone"}},
			{Step: harness.Step{Name: "new-session", Run: func(c *harness.Ctx) {
				c.Say("/new")
				c.WaitFor("New session started")
				c.Quiet(c.Settle())
				c.Snap("new")
				prompt(c, "A fresh start", 1)
				c.Snap("new-followup")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
