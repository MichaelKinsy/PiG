package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's prompt check is `modelRuntime.hasConfiguredAuth(provider) || await modelRuntime.checkAuth(provider) !== undefined` (agent-session.ts
// prompt validation): the published snapshot first, then a live check. A credential removed after the pass still admits the prompt; a credential
// stored after the pass, which no pass has published, is found by the live check.
func TestValidatePromptModelAuthReadsTheSnapshotThenTheLiveCheck(t *testing.T) {
	for _, tc := range []struct {
		name              string
		storedAtPass, now bool
	}{
		{"credential removed after the pass", true, false},
		{"credential stored after the pass", false, true},
		{"no credential", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderEnvKeys(t)
			services := servicesWithDefaultTools(t, nil)
			if err := services.SettingsManager().SetDefaultModelAndProvider("openai", "gpt-4o"); err != nil {
				t.Fatal(err)
			}
			setStored := func(stored bool) {
				t.Helper()
				var err error
				if stored {
					err = services.Auth().Set("openai", ai.Credential{Type: ai.CredentialAPIKey, Key: "o-key"})
				} else {
					err = services.Auth().Delete(t.Context(), "openai")
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			setStored(tc.storedAtPass)
			if err := services.ModelRuntime().queueAvailabilityRefresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			setStored(tc.now)
			manager, err := NewInMemorySessionManager(services.CWD())
			if err != nil {
				t.Fatal(err)
			}
			result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, SessionManager: manager, Model: services.ModelRuntime().GetModel("openai", "gpt-4o")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = result.Session.Close() })
			err = result.Session.ValidatePromptModelAuth(t.Context())
			if want := tc.storedAtPass || tc.now; (err == nil) != want {
				t.Fatalf("ValidatePromptModelAuth = %v, Pi admits the prompt: %v", err, want)
			}
			if err != nil && !strings.Contains(err.Error(), "openai") {
				t.Fatalf("rejection %q does not name the provider", err)
			}
		})
	}
}
