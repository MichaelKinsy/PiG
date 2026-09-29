package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type observedLoginCredentialStore struct {
	ai.CredentialStore
	started chan struct{}
}

func (s observedLoginCredentialStore) Modify(ctx context.Context, providerID string, fn func(*ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	close(s.started)
	return s.CredentialStore.Modify(ctx, providerID, fn)
}

func TestLoginCancelWhileCredentialStoreLocked(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	authPath := filepath.Join(m.opts.AgentDir, "auth.json")
	store, err := ai.NewAuthStorage(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai", ai.Credential{Type: ai.CredentialAPIKey, Key: "old-key"}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	m.opts.ModelRegistry.runtimeCredentials = ai.NewRuntimeCredentials(observedLoginCredentialStore{CredentialStore: store, started: started})
	done := make(chan error, 1)
	go func() { done <- m.runAPIKeyLogin(tui.OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key"}) }()
	waitForRender(t, m.editorContainer, "Enter OpenAI API key")
	lockPath := authPath + ".lock"
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(lockPath) })
	deliverModalInput(t, m, []byte("new-key"))
	deliverModalInput(t, m, []byte("\r"))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		_ = os.Remove(lockPath)
		<-done
		t.Fatal("login bypassed the shared credential store")
	}
	deliverModalInput(t, m, []byte("\x1b"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		_ = os.Remove(lockPath)
		<-done
		t.Fatal("login did not cancel while waiting for credential lock")
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	credential, ok, err := store.GetRaw("openai")
	if err != nil || !ok || credential.Key != "old-key" {
		t.Fatalf("cancel changed stored credential: %+v %v %v", credential, ok, err)
	}
}
