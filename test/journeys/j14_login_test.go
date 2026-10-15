//go:build parity

package journeys

import (
	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

func init() {
	register(&harness.Journey{
		Name:       "login-logout",
		Doc:        "The /login method chooser and its cancellation, signing in with an API key for a provider (provider list, key prompt, stored credential in auth.json) and /logout removing it.",
		Touches:    []string{"internal/codingagent/", "coding/auth", "coding/", "tui/"},
		AgentFiles: []string{"auth.json"},
		// D80: PiG hides a typed secret by default; maskSecretInput=false restores Pi's plain-text prompt, which is the behavior compared here.
		Env: func(k harness.Kind) harness.Env {
			if k == harness.PiG {
				return harness.Env{Settings: map[string]any{"maskSecretInput": false}}
			}
			return harness.Env{}
		},
		Script: script(),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "login-chooser-cancel", Run: func(c *harness.Ctx) {
				c.Say("/login")
				c.WaitFor("Select authentication method")
				c.Quiet(c.Settle())
				c.Snap("chooser")
				c.Key("Down")
				c.Quiet(c.Settle())
				c.Snap("chooser-down")
				c.Esc()
				c.Quiet(c.Settle())
				c.Snap("chooser-cancelled")
			}}},
			{Step: harness.Step{Name: "api-key-login", Run: func(c *harness.Ctx) {
				c.Say("/login")
				c.WaitFor("Select authentication method")
				c.Key("Down")
				c.Key("Enter")
				c.WaitFor("Select provider")
				c.Quiet(c.Settle())
				c.Snap("provider-list")
				c.Type("openrouter")
				c.Quiet(c.Settle())
				c.Snap("provider-filtered")
				c.Key("Enter")
				c.WaitFor("API key")
				c.Quiet(c.Settle())
				c.Snap("key-prompt")
				c.Type("sk-journey-secret-0123456789")
				c.Quiet(c.Settle())
				c.Snap("key-typed")
				c.Key("Enter")
				c.WaitFor("Saved API key")
				c.Quiet(c.Settle())
				c.Snap("logged-in")
			}}},
			{Needs: []string{"api-key-login"}, Step: harness.Step{Name: "logout", Run: func(c *harness.Ctx) {
				c.Say("/logout")
				c.WaitFor("Select provider to logout")
				c.Quiet(c.Settle())
				c.Snap("logout-list")
				c.Key("Enter")
				c.WaitFor("Removed stored API key")
				c.Quiet(c.Settle())
				c.Snap("logged-out")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
