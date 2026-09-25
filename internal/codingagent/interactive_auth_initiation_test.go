package codingagent

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type initiationCaptureOAuthProvider struct {
	callbacks chan ai.OAuthLoginCallbacks
}

func (p *initiationCaptureOAuthProvider) ID() string                           { return "initiation-capture" }
func (p *initiationCaptureOAuthProvider) Name() string                         { return "Initiation Capture" }
func (p *initiationCaptureOAuthProvider) UsesCallbackServer() bool             { return false }
func (p *initiationCaptureOAuthProvider) GetAPIKey(ai.OAuthCredentials) string { return "" }
func (p *initiationCaptureOAuthProvider) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("unused")
}
func (p *initiationCaptureOAuthProvider) Login(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	p.callbacks <- callbacks
	return ai.OAuthCredentials{}, errors.New("stop after callback capture")
}

// Registered OAuth providers receive callback-owned initiation boundaries for
// every prompt that can wait on terminal input. The subprocess host can then
// release the ordered call lane after dialog installation, not after the user
// eventually answers.
func TestRegisteredOAuthUsesInitiationAwareCallbacks(t *testing.T) {
	provider := &initiationCaptureOAuthProvider{callbacks: make(chan ai.OAuthLoginCallbacks, 1)}
	mode := NewInteractiveMode(InteractiveOptions{AgentDir: t.TempDir()})
	if err := mode.runLoginRegisteredOAuth(t.Context(), provider, ""); err != nil {
		t.Fatal(err)
	}
	callbacks := <-provider.callbacks
	if callbacks.OnPromptContext == nil {
		t.Error("registered OAuth prompt has no initiation-aware callback")
	}
	if callbacks.OnManualCodeInputContext == nil {
		t.Error("registered OAuth manual-code input has no initiation-aware callback")
	}
	if callbacks.OnSelectContext == nil {
		t.Error("registered OAuth selection has no initiation-aware callback")
	}
}
