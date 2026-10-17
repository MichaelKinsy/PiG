//go:build parity

package journeys

import (
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

const compactionSummary = "## Goal\nJourney summary of the earlier conversation.\n\n## Progress\n- earlier turns were answered\n\n## Next Steps\n- continue"

func init() {
	register(&harness.Journey{
		Name:    "compaction",
		Doc:     "Manual /compact (plain and with focus), a cancelled compaction, an automatic compaction triggered by reported context usage, and conversation after each: summary requests, compaction entries, footer usage and the follow-up request transcripts.",
		Touches: []string{"coding/compaction", "coding/", "internal/codingagent/", "agent/", "tui/"},
		Env: func(harness.Kind) harness.Env {
			return harness.Env{Settings: map[string]any{"compaction": map[string]any{"enabled": true, "reserveTokens": 16384, "keepRecentTokens": 60}}}
		},
		Script: script(
			onSummary(func(r *harness.Request) harness.Reply {
				if r.Has("hold-the-summary") {
					return harness.Say("## Goal\nstarted ").Then(harness.Hold("summary")).Then(harness.Say("never"))
				}
				return harness.Say(compactionSummary)
			}),
			onPrompt("BIGCONTEXT", fixed(harness.Say("A reply that reports a nearly full context window.").WithUsage(120000, 400))),
			onPrompt("HOLD", fixed(harness.Say("Short answer."))),
		),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
			{Name: "build-history", Run: func(c *harness.Ctx) {
				for i, p := range []string{"First question about alpha", "Second question about beta", "Third question about gamma", "Fourth question about delta"} {
					prompt(c, p, 1)
					c.WaitFor("Echo: " + p)
					_ = i
				}
				c.Snap("history")
			}},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "compact-plain", Run: func(c *harness.Ctx) {
				// Right after build-history both products answer "Nothing to compact (session too small)" and send no summary request; the
				// other actions also start with a prompt.
				prompt(c, "A question before the plain compaction", 1)
				base := len(c.P.Requests())
				c.Say("/compact")
				c.Idle(base + 1)
				awaitCompactionBlock(c)
				c.Snap("compacted")
			}}},
			{Step: harness.Step{Name: "compact-focus", Run: func(c *harness.Ctx) {
				prompt(c, "Another question about epsilon", 1)
				base := len(c.P.Requests())
				c.Say("/compact focus on epsilon")
				c.Idle(base + 1)
				awaitCompactionBlock(c)
				c.Snap("compacted-focus")
			}}, Needs: []string{"compact-plain"}},
			{Step: harness.Step{Name: "compact-cancel", Run: func(c *harness.Ctx) {
				prompt(c, "A question before the cancelled compaction", 1)
				base := len(c.P.Requests())
				c.Say("/compact hold-the-summary")
				c.WaitGate("summary")
				c.Quiet(c.Settle())
				c.Snap("compacting")
				c.Key("Escape")
				c.Await("summary request cancelled", func(string) bool { return c.P.Count(harness.EvDisconnect) >= 1 })
				c.Idle(base + 1)
				c.Snap("compaction-cancelled")
				prompt(c, "HOLD continue after cancel", 1)
				c.Snap("after-cancel")
			}}},
			{Step: harness.Step{Name: "auto-compact", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("BIGCONTEXT fill the window")
				// The reply reports 120k of 128k tokens: Pi compacts automatically after the turn.
				c.Idle(base + 2)
				c.Await("automatic compaction finished", func(s string) bool { return strings.Contains(s, "Compacted") || strings.Contains(s, "compaction") })
				c.Quiet(c.Settle())
				c.Snap("auto-compacted")
				prompt(c, "Question after the automatic compaction", 1)
				c.Snap("after-auto")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}

// awaitCompactionBlock waits until a compaction block follows the last echoed reply. The summary request finishes before the product adds the
// block, so Idle alone can snap Pi between the two (seed 11), and "Compacted" alone is already on screen when an automatic compaction ran first.
func awaitCompactionBlock(c *harness.Ctx) {
	c.Await("compaction block after the last reply", func(s string) bool {
		return strings.LastIndex(s, "Compacted from") > strings.LastIndex(s, "Echo: ")
	})
}
