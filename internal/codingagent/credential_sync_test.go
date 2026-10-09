package codingagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

const brokenCustomProvider = `{"providers":{"%s":{"api":"openai-completions","apiKey":"x","models":[{"id":"m1"}]}}}`

func syncRuntime(t *testing.T, dir, providerID string, credentials ai.CredentialStore) *RequestAuthRuntime {
	t.Helper()
	body := strings.Replace(brokenCustomProvider, "%s", providerID, 1)
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRequestAuthRuntime(t.Context(), RequestAuthRuntimeOptions{Credentials: credentials, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

// model-runtime.ts:591-611 synchronizeCredentialState: recomposing the provider
// after its credential changed throws CredentialSynchronizationError when its
// models.json composition fails; the message names the operation and provider.
func TestSynchronizeCredentialStateReportsCompositionFailure(t *testing.T) {
	dir := t.TempDir()
	runtime := syncRuntime(t, dir, "myprov", ai.NewInMemoryAuthStorage(nil))
	for _, op := range []CredentialSynchronizationOperation{CredentialSynchronizationLogin, CredentialSynchronizationLogout} {
		err := runtime.SynchronizeCredentialState(t.Context(), "myprov", op, nil)
		syncErr, ok := errors.AsType[*CredentialSynchronizationError](err)
		if !ok || syncErr.ProviderID != "myprov" || syncErr.Operation != op {
			t.Fatalf("%s: err = %v", op, err)
		}
		want := "Credential " + string(op) + " committed for myprov, but local synchronization failed"
		if err.Error() != want || !strings.Contains(syncErr.Cause.Error(), `"baseUrl" is required when defining custom models`) {
			t.Fatalf("%s: message %q cause %v", op, err, syncErr.Cause)
		}
	}
	if err := runtime.SynchronizeCredentialState(t.Context(), "anthropic", CredentialSynchronizationLogin, nil); err != nil {
		t.Fatalf("a built-in provider without a models.json entry failed to synchronize: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runtime.SynchronizeCredentialState(cancelled, "anthropic", CredentialSynchronizationLogin, nil); err == nil {
		t.Fatal("a cancelled synchronization succeeded")
	}
}

// interactive-mode.ts:6012-6023: /logout commits the removal, then reports a
// synchronization failure as "Credentials removed for <name>, but local model
// state could not be synchronized: <message>" instead of "Logout failed".
func TestLogoutReportsCredentialSynchronizationFailure(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	authPath := filepath.Join(m.opts.AgentDir, "auth.json")
	auth, err := ai.NewAuthStorage(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte(`{"myprov":{"type":"api_key","key":"sk-test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.RequestAuthRuntime = syncRuntime(t, m.opts.AgentDir, "myprov", auth)
	sc := m.buildSlashContext(t.Context())
	sc.LogoutProviders = func() ([]tui.OAuthProvider, error) {
		return []tui.OAuthProvider{{ID: "myprov", Name: "My Provider", AuthType: "api_key"}}, nil
	}
	sc.SelectAuthProvider = func(_ string, providers []tui.OAuthProvider, _ string) (tui.OAuthProvider, bool) {
		return providers[0], true
	}
	err = handleLogoutCommand(sc)
	want := "Credentials removed for My Provider, but local model state could not be synchronized: Credential logout committed for myprov, but local synchronization failed"
	if err == nil || err.Error() != want {
		t.Fatalf("logout error = %v, want %q", err, want)
	}
	if _, ok, _ := auth.Get("myprov"); ok {
		t.Fatal("the committed removal was rolled back")
	}
}

// interactive-mode.ts:6199-6203: a synchronization failure after an API key login shows
// "Saved API key for <name>, but local model state could not be synchronized: <message>".
func TestAPIKeyLoginReportsCredentialSynchronizationFailure(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	method := ai.EnvAPIKeyAuth("Sync probe token")
	method.Login = func(_ context.Context, _ ai.AuthInteraction) (ai.Credential, error) {
		return ai.Credential{Type: ai.CredentialAPIKey, Key: "sk-test"}, nil
	}
	if err := m.opts.ModelRegistry.RegisterNativeModelsProvider(&ai.ModelsProvider{ID: "synctest", Name: "Sync Test", Auth: ai.ProviderAuth{APIKey: method}, GetModels: func() ([]*ai.Model, error) { return nil, nil }}); err != nil {
		t.Fatal(err)
	}
	m.opts.RequestAuthRuntime = syncRuntime(t, m.opts.AgentDir, "synctest", ai.NewInMemoryAuthStorage(nil))
	if err := m.runAPIKeyLogin(tui.OAuthProvider{ID: "synctest", Name: "Sync Test", AuthType: "api_key"}); err != nil {
		t.Fatal(err)
	}
	text := plainRender(m.chatContainer)
	want := "Saved API key for Sync Test, but local model state could not be synchronized: Credential login committed for synctest, but local synchronization failed"
	if !strings.Contains(text, want) || strings.Contains(text, "Failed to save API key") {
		t.Fatalf("transcript = %q, want %q", text, want)
	}
}

// failingReadStore fails Read for one provider, as a stored credential whose value cannot be resolved does.
type failingReadStore struct {
	ai.CredentialStore
	failing string
}

func (s failingReadStore) Read(ctx context.Context, providerID string) (*ai.Credential, error) {
	if providerID == s.failing {
		return nil, errors.New("credential unreadable")
	}
	return s.CredentialStore.Read(ctx, providerID)
}

// model-runtime.ts:380-431 refreshProviderAvailability reads only the changed provider: another provider's unreadable
// credential does not fail its synchronization, and its own does.
func TestSynchronizeCredentialStateChecksOnlyTheChangedProvider(t *testing.T) {
	store := failingReadStore{CredentialStore: ai.NewInMemoryAuthStorage(nil), failing: "openai"}
	runtime := syncRuntime(t, t.TempDir(), "myprov", store)
	if err := runtime.SynchronizeCredentialState(t.Context(), "anthropic", CredentialSynchronizationLogout, nil); err != nil {
		t.Fatalf("an unrelated provider's credential failed anthropic's synchronization: %v", err)
	}
	err := runtime.SynchronizeCredentialState(t.Context(), "openai", CredentialSynchronizationLogin, nil)
	if syncErr, ok := errors.AsType[*CredentialSynchronizationError](err); !ok || syncErr.Cause.Error() != "credential unreadable" {
		t.Fatalf("openai synchronization = %v", err)
	}
	if !strings.Contains(runtime.GetError(), "Availability refresh: credential unreadable") {
		t.Fatalf("availability error = %q", runtime.GetError())
	}
	if err := runtime.SynchronizeCredentialState(t.Context(), "anthropic", CredentialSynchronizationLogin, nil); err != nil || strings.Contains(runtime.GetError(), "Availability refresh") {
		t.Fatalf("a later success kept the availability error: %v, %q", err, runtime.GetError())
	}
}

// Synchronization runs on the owner loop while the Anthropic subscription warning checks auth off the loop
// (maybeWarnAboutAnthropicSubscriptionAuthAsync); it must not rewrite the provider maps that check reads. Run with -race.
func TestSynchronizeCredentialStateLeavesProvidersForConcurrentAuthChecks(t *testing.T) {
	runtime := syncRuntime(t, t.TempDir(), "myprov", ai.NewInMemoryAuthStorage(nil))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = runtime.CheckAuth(t.Context(), "anthropic")
			}
		}
	})
	for range 50 {
		_ = runtime.SynchronizeCredentialState(t.Context(), "anthropic", CredentialSynchronizationLogin, nil)
		_ = runtime.SynchronizeCredentialState(t.Context(), "myprov", CredentialSynchronizationLogout, nil)
	}
	close(stop)
	wg.Wait()
}

// A login whose provider synchronizes completes normally (interactive-mode.ts:6191-6193): no synchronization error
// from an unrelated provider's composition failure.
func TestAPIKeyLoginCompletesWhenTheProviderSynchronizes(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	method := ai.EnvAPIKeyAuth("Sync probe token")
	method.Login = func(_ context.Context, _ ai.AuthInteraction) (ai.Credential, error) {
		return ai.Credential{Type: ai.CredentialAPIKey, Key: "sk-test"}, nil
	}
	if err := m.opts.ModelRegistry.RegisterNativeModelsProvider(&ai.ModelsProvider{ID: "synctest", Name: "Sync Test", Auth: ai.ProviderAuth{APIKey: method}, GetModels: func() ([]*ai.Model, error) { return nil, nil }}); err != nil {
		t.Fatal(err)
	}
	m.opts.RequestAuthRuntime = syncRuntime(t, m.opts.AgentDir, "myprov", ai.NewInMemoryAuthStorage(nil))
	if err := m.runAPIKeyLogin(tui.OAuthProvider{ID: "synctest", Name: "Sync Test", AuthType: "api_key"}); err != nil {
		t.Fatal(err)
	}
	if text := plainRender(m.chatContainer); strings.Contains(text, "could not be synchronized") || strings.Contains(text, "Failed to save API key") {
		t.Fatalf("transcript = %q", text)
	}
}

// interactive-mode.ts:6322-6326: an OAuth login names the login, not a saved API key, in the synchronization error.
func TestOAuthLoginSynchronizationFailureNamesTheLogin(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.opts.RequestAuthRuntime = syncRuntime(t, m.opts.AgentDir, "myprov", ai.NewInMemoryAuthStorage(nil))
	if !m.synchronizeLoginCredential(t.Context(), "myprov", "My Provider", ai.CredentialOAuth, nil) {
		t.Fatal("synchronization succeeded for an uncomposable provider")
	}
	want := "Logged in to My Provider, but local model state could not be synchronized: Credential login committed for myprov, but local synchronization failed"
	if text := plainRender(m.chatContainer); !strings.Contains(text, want) {
		t.Fatalf("transcript = %q, want %q", text, want)
	}
}

// interactive-mode.ts:6312-6326 showLoginDialog: a registered OAuth login whose provider cannot be recomposed shows the
// synchronization error and skips completing the authentication (no model selection).
func TestRegisteredOAuthLoginReportsCredentialSynchronizationFailure(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	m.opts.RequestAuthRuntime = syncRuntime(t, m.opts.AgentDir, "custom-oauth", ai.NewInMemoryAuthStorage(nil))
	provider := successfulLoginProvider{parityOAuthProvider{id: "custom-oauth", name: "Custom OAuth"}}
	if err := m.runLoginRegisteredOAuth(t.Context(), provider, ""); err != nil {
		t.Fatal(err)
	}
	text := plainRender(m.chatContainer)
	want := "Logged in to Custom OAuth, but local model state could not be synchronized: Credential login committed for custom-oauth, but local synchronization failed"
	if !strings.Contains(text, want) || strings.Contains(text, "no default model is configured") {
		t.Fatalf("transcript = %q, want %q", text, want)
	}
}
