package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Implementation-derived (Pi 1.0.0 adds no test for the wording): with no provider of the chosen auth type, /login
// reports "No account providers available." for sign-in and "No API key providers available." for API keys
// (.upstream/v1.0.0/packages/coding-agent/src/modes/interactive/interactive-mode.ts:5886-5895).
func TestLoginWithoutProvidersOfTheChosenAuthTypeSaysSoUpstream(t *testing.T) {
	for _, tc := range []struct {
		chosen    string
		providers []tui.OAuthProvider
		want      string
	}{
		{"oauth", []tui.OAuthProvider{{ID: "openai", Name: "OpenAI", AuthType: "api_key"}}, "No account providers available."},
		{"api_key", []tui.OAuthProvider{{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"}}, "No API key providers available."},
	} {
		t.Run(tc.chosen, func(t *testing.T) {
			var statuses []string
			sc := &SlashContext{
				ShowStatus: func(message string) { statuses = append(statuses, message) },
				Append:     func(message string) { t.Errorf("appended %q", message) },
			}
			sc.LoginProviders = func() []tui.OAuthProvider { return tc.providers }
			sc.SelectAuthMethod = func([]tui.OAuthProvider) (string, bool) { return tc.chosen, true }
			sc.SelectAuthProvider = func(string, []tui.OAuthProvider, string) (tui.OAuthProvider, bool) {
				t.Error("opened the provider selector without providers")
				return tui.OAuthProvider{}, false
			}
			sc.StartProviderLogin = func(tui.OAuthProvider) error {
				t.Error("started a login without providers")
				return nil
			}
			if err := handleLoginCommand(sc); err != nil {
				t.Fatal(err)
			}
			if want := []string{tc.want}; !slices.Equal(statuses, want) {
				t.Fatalf("statuses = %q, want %q", statuses, want)
			}
		})
	}
}
