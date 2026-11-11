//go:build parity

package journeys

import (
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

// An extension's autocomplete provider that answers without I/O: it awaits the current provider (a command's synchronous argument completions) and returns the answer.
// Pi runs the extension in its own process and resolves the wrapper chain in the Promise jobs that follow the read, so the menu is open when the next read arrives.
const passThroughAutocompleteExtension = `export default function (pi) {
  pi.on("session_start", (_, ctx) => ctx.ui.addAutocompleteProvider(current => ({
    async getSuggestions(lines, line, col, options) {
      return await current.getSuggestions(lines, line, col, options);
    },
    applyCompletion: (...args) => current.applyCompletion(...args),
  })));
}
`

func init() {
	register(&harness.Journey{
		Name: "extension-completion-fast-enter",
		Doc: "completion-fast-enter with an extension autocomplete provider installed (ctx.ui.addAutocompleteProvider) that answers without I/O: Pi resolves the whole provider chain in the Promise jobs that follow the read that typed the text, " +
			"so the Enter sent as the next read accepts the menu's suggestion and only a second Enter submits (interactive-mode.ts setupAutocompleteProvider, editor.ts startAutocompleteRequest).",
		Touches:     []string{"internal/codingagent/", "coding/extension/", "tui/"},
		MemMaxMB:    400, // the packed Node extension runtime is a second process
		Normalizers: []harness.Normalizer{bugReportHint},
		Env: func(harness.Kind) harness.Env {
			return harness.Env{
				NoModelArg:    true,
				ReadyMarker:   fastEnterFirst,
				Reasoning:     true,
				Models:        []string{fastEnterFirst, fastEnterSecond},
				Extensions:    true,
				AgentDirFiles: map[string]string{"extensions/pass-through-autocomplete.ts": passThroughAutocompleteExtension},
			}
		},
		Script: script(),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) {
				c.Launch("--model", "strict-wire/"+fastEnterFirst)
				// session_start installs the wrapper after the footer is drawn; typing before that is cancelled by the provider swap.
				c.Quiet(c.Settle())
				c.Quiet(c.Settle())
				c.Snap("startup")
			}},
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
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
