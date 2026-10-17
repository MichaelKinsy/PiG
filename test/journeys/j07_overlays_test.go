//go:build parity

package journeys

import (
	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

func init() {
	register(&harness.Journey{
		Name:        "overlays-selectors-settings",
		Doc:         "Slash-command autocomplete, /settings (toggle, search, persistence), /model, /scoped-models and /hotkeys: each selector or overlay opens over a live conversation, is driven with the keyboard, closes, and leaves the editor and the settings file as Pi does.",
		Touches:     []string{"internal/codingagent/", "tui/", "coding/", "internal/coding/"},
		AgentFiles:  []string{"settings.json"},
		Normalizers: []harness.Normalizer{d80SettingsCount},
		Script:      script(),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
			{Name: "one-turn", Run: func(c *harness.Ctx) { prompt(c, "Show me the settings next", 1); c.Snap("turn") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "slash-autocomplete", Run: func(c *harness.Ctx) {
				c.Type("/")
				c.WaitFor("(1/")
				c.Quiet(c.Settle())
				c.Snap("menu")
				c.Key("Down")
				c.Key("Down")
				c.Quiet(c.Settle())
				c.Snap("menu-moved")
				c.Type("tre")
				c.WaitFor("/tre")
				c.Quiet(c.Settle())
				c.Snap("menu-filtered")
				c.Esc()
				c.Key("C-c") // clear the editor
				c.Quiet(c.Settle())
				c.Snap("menu-closed")
			}}},
			{Step: harness.Step{Name: "settings-toggle", Run: func(c *harness.Ctx) {
				c.Say("/settings")
				c.WaitFor("Auto-compact")
				c.Quiet(c.Settle())
				c.Snap("settings")
				c.Key("Down")
				c.Key("Down")
				c.Key("Enter") // Block images
				c.Quiet(c.Settle())
				c.Snap("settings-toggled")
				c.Key("Down")
				c.Key("Space") // Skill commands
				c.Quiet(c.Settle())
				c.Snap("settings-toggled-2")
				c.Esc()
				c.WaitGone("Auto-compact")
				c.Snap("settings-closed")
			}}},
			{Step: harness.Step{Name: "settings-search", Run: func(c *harness.Ctx) {
				c.Say("/settings")
				c.WaitFor("Auto-compact")
				c.Type("padding")
				c.WaitFor("> padding")
				c.Quiet(c.Settle())
				c.Snap("settings-filtered")
				c.Esc()
				c.Quiet(c.Settle())
				c.Esc()
				c.WaitGone("Auto-compact")
				c.Snap("settings-search-closed")
			}}},
			{Step: harness.Step{Name: "model-selector", Run: func(c *harness.Ctx) {
				c.Say("/model")
				c.WaitFor("Only showing models from configured providers", "Model catalogs refreshed.")
				c.Quiet(c.Settle())
				c.Snap("model")
				c.Esc()
				c.WaitGone("Only showing models")
				c.Snap("model-closed")
			}}},
			{Step: harness.Step{Name: "scoped-models", Run: func(c *harness.Ctx) {
				c.Say("/scoped-models")
				c.WaitFor("Model Configuration", "Model catalogs refreshed.")
				c.Quiet(c.Settle())
				c.Snap("scoped")
				c.Key("Enter") // toggle the only model
				c.Quiet(c.Settle())
				c.Snap("scoped-toggled")
				c.Key("Enter")
				c.Esc()
				c.WaitGone("Model Configuration")
				c.Snap("scoped-closed")
			}}},
			{Step: harness.Step{Name: "hotkeys", Run: func(c *harness.Ctx) {
				c.Say("/hotkeys")
				c.WaitFor("Run bash command (excluded from context)")
				c.Quiet(c.Settle())
				c.Snap("hotkeys")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
