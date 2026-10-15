package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// deviceIDRecorder is an OAuth provider whose login records the device ID its callbacks offer.
type deviceIDRecorder struct {
	ai.OAuthProviderInterface
	got string
}

func (p *deviceIDRecorder) Login(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	if callbacks.GetDeviceID != nil {
		p.got = callbacks.GetDeviceID()
	}
	return ai.OAuthCredentials{}, nil
}

// Pi passes the installation's device ID to every login (interactive-mode.ts:6297); Sign in with ChatGPT fails
// without it (#146).
func TestCLILoginPassesTheDeviceID(t *testing.T) {
	provider := &deviceIDRecorder{}
	if _, err := runOAuthProviderLogin(provider, true, func() string { return "0b8f3f84-4c1a-4f0e-9e57-6f1d2b2c9a10" }); err != nil {
		t.Fatal(err)
	}
	if provider.got != "0b8f3f84-4c1a-4f0e-9e57-6f1d2b2c9a10" {
		t.Fatalf("login got device ID %q", provider.got)
	}
}

// idRecordingProvider is a registered OAuth provider that records the device ID its login is offered.
type idRecordingProvider struct {
	id  string
	got *string
}

func (p idRecordingProvider) ID() string               { return p.id }
func (p idRecordingProvider) Name() string             { return p.id }
func (p idRecordingProvider) UsesCallbackServer() bool { return false }
func (p idRecordingProvider) Login(cb ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	if cb.GetDeviceID != nil {
		*p.got = cb.GetDeviceID()
	}
	return ai.OAuthCredentials{Access: "token", Expires: 4102444800000}, nil
}
func (p idRecordingProvider) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("unused")
}
func (p idRecordingProvider) GetAPIKey(c ai.OAuthCredentials) string { return c.Access }

// pig login <provider> reaches each provider's Login with the settings.json device ID (#146).
func TestRunLoginCommandPassesSettingsDeviceID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	const seeded = "0b8f3f84-4c1a-4f0e-9e57-6f1d2b2c9a10"
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"deviceId":"`+seeded+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if want := codingagent.NewSettingsManager(cwd, dir).GetOrCreateDeviceID(); want != seeded {
		t.Fatalf("settings device ID = %q, seeded %q", want, seeded)
	}
	for _, id := range []string{"device-id-probe-a", "device-id-probe-b"} {
		var got string
		provider := idRecordingProvider{id: id, got: &got}
		ai.RegisterOAuthProvider(id, provider)
		t.Cleanup(func() { ai.UnregisterOAuthProvider(id) })
		stdout, stderr, code := captureWithStdin(t, "", func() int {
			return runLoginCommand([]string{"login", id, "--no-input"})
		})
		if code != 0 {
			t.Fatalf("%s login code=%d stdout=%s stderr=%s", id, code, stdout, stderr)
		}
		if got != seeded {
			t.Fatalf("%s login got device ID %q, want %q", id, got, seeded)
		}
	}
}
