//go:build parity

package journeys

import (
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

// The models of the scripted provider: the one the session starts on and the one the fast `/model` selects.
const (
	fastEnterFirst  = "qwen3.8-q4-uncensored-layer"
	fastEnterSecond = "qwen3.8-q6-uncensored-tensor"
)

// fastEnterEditorHolds reports whether the typed command is still on the screen, in the editor.
func fastEnterEditorHolds(c *harness.Ctx, text string) bool { return strings.Contains(c.Screen(), text) }

func init() {
	register(&harness.Journey{
		Name: "completion-fast-enter",
		Doc: "Text and Enter sent right behind each other while an autocomplete menu is due, the way a script or a fast typist sends them (the Enter is the very next terminal read after the text's): Pi's editor opens the menu in the Promise jobs that follow the read that typed the text, " +
			"so the first Enter accepts the suggestion and only a second Enter submits (editor.ts requestAutocomplete/startAutocompleteRequest, tui.ts handleTerminalInput). " +
			"A `/model provider/id` and a `/thinking level` argument completion stay in the editor after the first Enter and apply on the second; a `/settin` command-name completion is accepted and submitted by the first Enter.",
		Touches:     []string{"internal/codingagent/", "tui/"},
		Normalizers: []harness.Normalizer{bugReportHint},
		Env: func(harness.Kind) harness.Env {
			return harness.Env{
				NoModelArg:  true,
				ReadyMarker: fastEnterFirst,
				Reasoning:   true,
				Models:      []string{fastEnterFirst, fastEnterSecond},
			}
		},
		Script: script(),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch("--model", "strict-wire/"+fastEnterFirst); c.Snap("startup") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "model-argument", Run: func(c *harness.Ctx) {
				spec := "strict-wire/" + fastEnterSecond
				c.SayNextRead("/model " + spec)
				c.Quiet(c.Settle())
				c.Quiet(c.Settle())
				c.Snap("model-first-enter")
				c.Expect(fastEnterEditorHolds(c, "/model "+spec), "the first Enter must accept the argument completion and leave %q in the editor", "/model "+spec)
				c.Expect(!strings.Contains(c.Screen(), "Model: "+fastEnterSecond), "the first Enter must not switch the model")
				c.Key("Enter")
				c.WaitFor("Model: " + fastEnterSecond)
				c.Quiet(c.Settle())
				c.Snap("model-second-enter")
			}}},
			{Step: harness.Step{Name: "thinking-argument", Run: func(c *harness.Ctx) {
				c.SayNextRead("/thinking high")
				c.Quiet(c.Settle())
				c.Quiet(c.Settle())
				c.Snap("thinking-first-enter")
				c.Expect(fastEnterEditorHolds(c, "/thinking high"), "the first Enter must accept the argument completion and leave the command in the editor")
				c.Key("Enter")
				c.WaitFor("Thinking level: high")
				c.Quiet(c.Settle())
				c.Snap("thinking-second-enter")
			}}},
			{Step: harness.Step{Name: "command-name", Run: func(c *harness.Ctx) {
				c.SayNextRead("/settin")
				c.Quiet(c.Settle())
				c.Quiet(c.Settle())
				c.Snap("command-name-enter")
				c.Expect(strings.Contains(c.Screen(), "Auto-compact"), "the first Enter must accept the command-name completion and submit /settings")
				c.Esc()
				c.WaitGone("Auto-compact")
				c.Snap("command-name-closed")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
