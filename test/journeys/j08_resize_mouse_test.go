//go:build parity

package journeys

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

func init() {
	register(&harness.Journey{
		Name:        "resize-mouse-scroll",
		Doc:         "Terminal resizes while idle, while an answer streams and under an overlay; mouse clicks expand and collapse a tool result, the wheel scrolls the fullscreen transcript and the jump-to-latest button returns to the bottom (fullscreen mode, mouse regions of tool-execution.ts).",
		Touches:     []string{"tui/", "internal/codingagent/"},
		Normalizers: []harness.Normalizer{d80SettingsCount},
		Script: script(
			onPrompt("LONGOUT", fixed(harness.Say("Listing numbers.").Then(harness.Use(
				harness.Call{Name: "bash", Args: map[string]any{"command": "seq 1 30"}})))),
			afterTools("LONGOUT", func(r *harness.Request, _ []harness.Message) harness.Reply {
				return harness.Say("Listed thirty numbers for " + strings.Fields(r.LastUser())[1] + ".")
			}),
			onPrompt("FILL", func(*harness.Request) harness.Reply {
				var b strings.Builder
				for i := 1; i <= 40; i++ {
					fmt.Fprintf(&b, "filler line %d\n\n", i)
				}
				return harness.Say(b.String())
			}),
			onPrompt("LONGSTREAM", fixed(harness.Say("A paragraph that is long enough to wrap differently at every width: the quick brown fox jumps over the lazy dog, again and again. ").Then(harness.Hold("resize")).Then(harness.Say("Tail after the resize.")))),
		),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
			{Name: "fill-transcript", Run: func(c *harness.Ctx) {
				c.Say("FILL the transcript")
				c.WaitFor("filler line 40")
				c.Quiet(c.Settle())
				c.Snap("filled")
			}},
			{Name: "long-output", Run: func(c *harness.Ctx) {
				c.Say("LONGOUT first")
				c.WaitFor("Listed thirty numbers for first.")
				c.Quiet(c.Settle())
				c.Snap("long-output")
			}},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "resize-idle", Run: func(c *harness.Ctx) {
				for _, size := range [][2]int{{60, 20}, {140, 40}, {80, 24}} {
					c.Resize(size[0], size[1])
					c.Quiet(c.Settle())
					c.Snap("resized")
				}
			}}},
			{Step: harness.Step{Name: "resize-streaming", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("LONGSTREAM now")
				c.WaitGate("resize")
				c.WaitFor("A paragraph that")
				c.Resize(50, 18)
				c.Quiet(c.Settle())
				c.Snap("narrow-streaming")
				c.Resize(100, 30)
				c.Quiet(c.Settle())
				c.Snap("wide-streaming")
				c.P.Release("resize")
				c.WaitFor("Tail after the resize.")
				c.Idle(base + 1)
				c.Resize(80, 24)
				c.Quiet(c.Settle())
				c.Snap("streamed")
			}}},
			{Step: harness.Step{Name: "resize-overlay", Run: func(c *harness.Ctx) {
				c.Say("/settings")
				c.WaitFor("Auto-compact")
				c.Resize(70, 16)
				c.Quiet(c.Settle())
				c.Snap("overlay-small")
				c.Resize(120, 36)
				c.Quiet(c.Settle())
				c.Snap("overlay-large")
				c.Esc()
				c.WaitGone("Auto-compact")
				c.Resize(80, 24)
				c.Quiet(c.Settle())
				c.Snap("overlay-closed")
			}}},
			{Step: harness.Step{Name: "click-expand-collapse", Run: func(c *harness.Ctx) {
				c.Say("LONGOUT click")
				c.WaitFor("Listed thirty numbers for click.")
				c.Quiet(c.Settle())
				c.Snap("fresh-result")
				collapsed := strings.Count(c.Screen(), "earlier lines")
				c.ClickText("earlier lines")
				c.Await("the clicked result expanded", func(s string) bool { return strings.Count(s, "earlier lines") < collapsed })
				c.Quiet(c.Settle())
				c.Snap("expanded")
				c.ClickText("Took")
				c.Await("the second click collapsed it", func(s string) bool { return strings.Count(s, "earlier lines") == collapsed })
				c.Quiet(c.Settle())
				c.Snap("collapsed-again")
			}}},
			{Step: harness.Step{Name: "wheel-and-jump", Run: func(c *harness.Ctx) {
				c.Wheel(10, 10, true)
				c.WaitFor("Jump to latest message")
				c.Quiet(c.Settle())
				c.Snap("scrolled-up")
				c.Wheel(10, 10, false)
				c.Quiet(c.Settle())
				c.Snap("scrolled-down")
				c.Wheel(10, 10, true)
				c.WaitFor("Jump to latest message")
				c.ClickText("Jump to latest message")
				c.WaitGone("Jump to latest message")
				c.Quiet(c.Settle())
				c.Snap("jumped")
				// The transient scrollbar hides on a 1s timer (scroll-view.ts scrollbarHideDelayMs): later frames are compared without it.
				c.Await("the scrollbar to hide", func(s string) bool { return !hasScrollbar(s) })
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}

// hasScrollbar reports whether the rightmost column of any row holds a scrollbar track or thumb cell.
func hasScrollbar(screen string) bool {
	for line := range strings.SplitSeq(screen, "\n") {
		if strings.HasSuffix(line, "│") || strings.HasSuffix(line, "┃") || strings.HasSuffix(line, "█") {
			return true
		}
	}
	return false
}
