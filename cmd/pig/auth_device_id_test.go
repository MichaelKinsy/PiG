package main

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
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
