package codingagent

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

const (
	deviceProbeURI = "https://github.com/login/device"
	authProbeURL   = "https://example.invalid/oauth/authorize?client_id=probe"
)

type deviceCodeOAuthProvider struct{ done chan struct{} }

func (deviceCodeOAuthProvider) ID() string                           { return "device-code-probe" }
func (deviceCodeOAuthProvider) Name() string                         { return "Device Code Probe" }
func (deviceCodeOAuthProvider) UsesCallbackServer() bool             { return false }
func (deviceCodeOAuthProvider) GetAPIKey(ai.OAuthCredentials) string { return "" }
func (deviceCodeOAuthProvider) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("unused")
}
func (p deviceCodeOAuthProvider) Login(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	defer close(p.done)
	callbacks.OnDeviceCode(ai.OAuthDeviceCodeInfo{UserCode: "WDJB-MJHT", VerificationURI: deviceProbeURI})
	callbacks.OnAuth(ai.OAuthAuthInfo{URL: authProbeURL})
	return ai.OAuthCredentials{}, errors.New("stop after the probe callbacks")
}

// Pi's notifyAuthDialog shows device_code through showDeviceCode/showWaiting, and only auth_url invokes the browser (interactive-mode.ts:6108-6114). The existing browser boundary is observed synchronously; the provider completion joins both callbacks.
func TestDeviceCodeLoginOpensNoBrowser(t *testing.T) {
	original := openBrowser
	t.Cleanup(func() { openBrowser = original })
	var launches []string
	openBrowser = func(url string) error { launches = append(launches, url); return nil }
	completed := make(chan struct{})
	m := newPostLoginTestMode(t)
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	done := make(chan error, 1)
	go func() {
		done <- m.runLoginRegisteredOAuth(t.Context(), deviceCodeOAuthProvider{done: completed}, "Device Code Probe", "")
	}()
	<-completed
	if err := <-done; err == nil || err.Error() != "Failed to login to Device Code Probe: stop after the probe callbacks" {
		t.Fatalf("rejected login err = %v", err)
	}
	if len(launches) != 1 || launches[0] != authProbeURL {
		t.Fatalf("browser launches = %q, want only the auth URL %s", launches, authProbeURL)
	}
}
