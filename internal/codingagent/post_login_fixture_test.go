package codingagent

import (
	"path/filepath"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// setPostLoginAPIKey supplies the persisted credential boundary to post-login state-machine tests. Login dialog tests drive the command and provider callbacks separately.
func setPostLoginAPIKey(m *InteractiveMode, provider, value string) error {
	previous := m.opts.Model
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return err
	}
	if err := auth.Set(provider, ai.Credential{Type: ai.CredentialAPIKey, Key: value}); err != nil {
		return err
	}
	if m.opts.ModelRegistry != nil {
		m.opts.ModelRegistry.Refresh()
	}
	var redact func(string) string
	if m.maskSecretInput() {
		redact = func(text string) string { return tui.RedactSecretInput(text, value) }
	}
	m.completeProviderAuthentication(provider, buildAuthProviderName(provider), ai.CredentialAPIKey, previous, auth.Path(), redact)
	return nil
}
