package codingagent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type cancellableLoginProvider struct {
	parityOAuthProvider
	stopped chan struct{}
}

func (p cancellableLoginProvider) LoginContext(ctx context.Context, callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	defer close(p.stopped)
	_, err := callbacks.OnPromptContext(ctx, ai.OAuthPrompt{Message: "Enter login code:"})
	return ai.OAuthCredentials{}, err
}

// Pi login-dialog.ts:83-90 cancels its pending prompt. No credentials or model selection survive cancellation, and a later login owns a fresh dialog.
func TestOAuthLoginCancellationDoesNotCompleteAuthentication(t *testing.T) {
	for _, key := range []string{"\x1b", "\x03"} {
		t.Run(key, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
			provider := cancellableLoginProvider{parityOAuthProvider{id: "anthropic", name: "Anthropic"}, make(chan struct{})}
			done := make(chan error, 1)
			go func() { done <- m.runLoginRegisteredOAuth(t.Context(), provider, "") }()
			waitForRender(t, m.editorContainer, "Enter login code:")
			deliverModalInput(t, m, []byte(key))
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled login did not return")
			}
			<-provider.stopped
			if m.opts.Model != nil || strings.Contains(plainRender(m.chatContainer), "Logged in") {
				t.Fatal("cancelled login completed authentication")
			}
			auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			if _, stored, err := auth.Get("anthropic"); stored || err != nil {
				t.Fatalf("cancelled credential stored=%v, err=%v", stored, err)
			}
			if err := m.runLoginRegisteredOAuth(t.Context(), successfulLoginProvider{provider.parityOAuthProvider}, ""); err != nil {
				t.Fatal(err)
			}
			waitPostLoginStatus(t, m, "Logged in to Anthropic. Selected claude-opus-4-8.")
		})
	}
}
