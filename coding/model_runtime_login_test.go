package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// loginTestRuntime builds a ModelRuntime on an in-memory credential store and an optional models.json inside a temp agent dir. It never reads ~/.pig or a real auth.json.
func loginTestRuntime(t *testing.T, modelsJSON string) (*ModelRuntime, ai.CredentialStore) {
	t.Helper()
	// The ambient environment must not configure the providers under test.
	clearProviderEnvKeys(t)
	credentials := ai.NewInMemoryCredentialStore()
	modelsPath := new((*string)(nil))
	if modelsJSON != "" {
		path := filepath.Join(t.TempDir(), "models.json")
		if err := os.WriteFile(path, []byte(modelsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		modelsPath = new(&path)
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
	t.Setenv("LOGIN_TEST_API_KEY", "")
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{Credentials: credentials, ModelsPath: modelsPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	return runtime, credentials
}

func dummyKeyInteraction(key string) ai.AuthInteraction {
	return ai.AuthInteraction{Prompt: func(context.Context, ai.AuthPrompt) (string, error) { return key, nil }, Notify: func(ai.AuthEvent) {}}
}

// model-runtime.ts:817-829 login(providerId, type, interaction, options) delegates to the provider collection that holds every composed provider, built-in ones included, then synchronizes. ModelRuntime.Login must resolve the provider GetProvider returns for the same ID.
func TestModelRuntimeLoginResolvesComposedProviders(t *testing.T) {
	const modelsJSON = `{"providers":{"login-custom":{"baseUrl":"https://custom.invalid/v1","apiKey":"$LOGIN_TEST_API_KEY","api":"openai-completions","models":[{"id":"custom-model"}]}}}`
	cases := []struct {
		name  string
		id    string
		setup func(t *testing.T, runtime *ModelRuntime)
	}{
		{name: "built-in", id: "anthropic"},
		{name: "models.json", id: "login-custom"},
		{name: "native", id: "login-native", setup: func(t *testing.T, runtime *ModelRuntime) {
			provider := nativeCompatProvider(nativeCompatModel("native-model", "login-native", "https://native.invalid/v1"))
			// Configured only by a stored credential, like the built-in and models.json shapes here.
			provider.Auth.APIKey = &ai.APIKeyAuth{Name: "Native key", Login: func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
				key, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "API key"})
				return ai.Credential{Type: ai.CredentialAPIKey, Key: key}, err
			}, Resolve: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
				if input.Credential == nil || input.Credential.Key == "" {
					return nil, nil
				}
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Source: "stored native key"}, nil
			}}
			if err := runtime.RegisterNativeProvider(provider); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime, credentials := loginTestRuntime(t, modelsJSON)
			if tc.setup != nil {
				tc.setup(t, runtime)
			}
			if runtime.GetProvider(tc.id) == nil {
				t.Fatalf("GetProvider(%q) = nil", tc.id)
			}
			if status := runtime.GetProviderAuthStatus(tc.id); status.Configured || runtime.HasConfiguredAuth(tc.id) {
				t.Fatalf("auth status before login = %+v, configured %v", status, runtime.HasConfiguredAuth(tc.id))
			}
			credential, err := runtime.Login(t.Context(), tc.id, ai.CredentialAPIKey, dummyKeyInteraction("dummy-key"))
			if err != nil {
				t.Fatalf("Login(%q) = %v", tc.id, err)
			}
			if credential.Type != ai.CredentialAPIKey || credential.Key != "dummy-key" {
				t.Fatalf("credential = %+v", credential)
			}
			saved, err := credentials.Read(t.Context(), tc.id)
			if err != nil || saved == nil || saved.Key != "dummy-key" {
				t.Fatalf("stored credential = %+v, %v", saved, err)
			}
			check, err := runtime.CheckAuth(t.Context(), tc.id)
			if err != nil || check == nil || check.Type != ai.CredentialAPIKey {
				t.Fatalf("CheckAuth after login = %+v, %v", check, err)
			}
			// synchronizeCredentialState (model-runtime.ts:591-610) publishes availability before login resolves.
			if status := runtime.GetProviderAuthStatus(tc.id); !status.Configured || status.Source != ai.AuthSourceStored || !runtime.HasConfiguredAuth(tc.id) {
				t.Fatalf("auth status after login = %+v, configured %v", status, runtime.HasConfiguredAuth(tc.id))
			}
			if !slices.ContainsFunc(runtime.GetAvailableSnapshot(), func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == tc.id }) {
				t.Fatalf("available snapshot after login lacks %s", tc.id)
			}
			if err := runtime.Logout(t.Context(), tc.id); err != nil {
				t.Fatalf("Logout(%q) = %v", tc.id, err)
			}
			if saved, err := credentials.Read(t.Context(), tc.id); err != nil || saved != nil {
				t.Fatalf("credential retained after logout = %+v, %v", saved, err)
			}
			if status := runtime.GetProviderAuthStatus(tc.id); status.Configured || runtime.HasConfiguredAuth(tc.id) {
				t.Fatalf("auth status after logout = %+v, configured %v", status, runtime.HasConfiguredAuth(tc.id))
			}
		})
	}
}

// models.ts:767-774 rejects an unknown provider ID with ModelsError("provider") and a method the provider lacks with ModelsError("auth"), both before any prompt.
func TestModelRuntimeLoginRejectsUnknownProviderAndUnsupportedMethod(t *testing.T) {
	runtime, credentials := loginTestRuntime(t, "")
	prompted := false
	interaction := ai.AuthInteraction{Prompt: func(context.Context, ai.AuthPrompt) (string, error) { prompted = true; return "key", nil }}
	var modelsErr *ai.ModelsError
	_, err := runtime.Login(t.Context(), "no-such-provider", ai.CredentialAPIKey, interaction)
	if !errors.As(err, &modelsErr) || modelsErr.Code != ai.ModelsErrorProvider || modelsErr.Message != "Unknown provider: no-such-provider" {
		t.Fatalf("unknown provider error = %#v", err)
	}
	_, err = runtime.Login(t.Context(), "openai-codex", ai.CredentialAPIKey, interaction)
	if !errors.As(err, &modelsErr) || modelsErr.Code != ai.ModelsErrorAuth || !strings.HasSuffix(modelsErr.Message, " does not support api_key login") {
		t.Fatalf("unsupported method error = %#v", err)
	}
	if prompted {
		t.Fatal("login prompted before method validation")
	}
	if saved, err := credentials.Read(t.Context(), "no-such-provider"); err != nil || saved != nil {
		t.Fatalf("credential stored for unknown provider = %+v, %v", saved, err)
	}
}

// model-runtime.ts:817-825 forwards LoginOptions to the provider's OAuth login, so an embedder can pass GetDeviceID. Omitting the options passes the zero value.
func TestModelRuntimeLoginForwardsLoginOptions(t *testing.T) {
	runtime, _ := loginTestRuntime(t, "")
	var got []ai.LoginOptions
	provider := nativeCompatProvider(nativeCompatModel("oauth-model", "login-oauth", "https://oauth.invalid/v1"))
	provider.Auth.OAuth = &ai.OAuthAuth{
		Name: "Login OAuth",
		Login: func(_ context.Context, _ ai.AuthInteraction, options ai.LoginOptions) (ai.Credential, error) {
			got = append(got, options)
			return ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
		},
		Refresh: func(_ context.Context, current ai.Credential) (ai.Credential, error) { return current, nil },
		ToAuth:  func(current ai.Credential) (ai.ModelAuth, error) { return ai.ModelAuth{APIKey: current.Access}, nil },
	}
	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Login(t.Context(), provider.ID, ai.CredentialOAuth, ai.AuthInteraction{}, ai.LoginOptions{GetDeviceID: func() string { return "device-1" }}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Login(t.Context(), provider.ID, ai.CredentialOAuth, ai.AuthInteraction{}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].GetDeviceID == nil || got[0].GetDeviceID() != "device-1" || got[1].GetDeviceID != nil {
		t.Fatalf("forwarded options = %+v", got)
	}
}

// model-runtime.ts:817-829 serializes same-provider credential operations through enqueueCredentialOperation. A second login on a built-in or models.json provider starts only after the first one finishes.
func TestModelRuntimeLoginSerializesComposedProvider(t *testing.T) {
	const modelsJSON = `{"providers":{"login-custom":{"baseUrl":"https://custom.invalid/v1","apiKey":"$LOGIN_TEST_API_KEY","api":"openai-completions","models":[{"id":"custom-model"}]}}}`
	for _, id := range []string{"anthropic", "login-custom"} {
		t.Run(id, func(t *testing.T) { testModelRuntimeLoginSerializes(t, id, modelsJSON) })
	}
}

func testModelRuntimeLoginSerializes(t *testing.T, id, modelsJSON string) {
	synctest.Test(t, func(t *testing.T) {
		runtime, _ := loginTestRuntime(t, modelsJSON)
		var mu sync.Mutex
		var order []string
		release := make(chan struct{})
		prompt := func(name string, block bool) ai.AuthInteraction {
			return ai.AuthInteraction{Prompt: func(ctx context.Context, _ ai.AuthPrompt) (string, error) {
				mu.Lock()
				order = append(order, name+":start")
				mu.Unlock()
				if block {
					select {
					case <-release:
					case <-ctx.Done():
						return "", context.Cause(ctx)
					}
				}
				mu.Lock()
				order = append(order, name+":end")
				mu.Unlock()
				return name, nil
			}}
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Go(func() {
			_, errs[0] = runtime.Login(t.Context(), id, ai.CredentialAPIKey, prompt("first", true))
		})
		synctest.Wait()
		wg.Go(func() {
			_, errs[1] = runtime.Login(t.Context(), id, ai.CredentialAPIKey, prompt("second", false))
		})
		// Every goroutine is durably blocked here, so a second login that is not queued behind the first has already prompted.
		synctest.Wait()
		mu.Lock()
		snapshot := strings.Join(order, ",")
		mu.Unlock()
		if snapshot != "first:start" {
			t.Fatalf("order while the first login prompts = %s, want first:start", snapshot)
		}
		close(release)
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("errors = %v, %v", errs[0], errs[1])
		}
		want := []string{"first:start", "first:end", "second:start", "second:end"}
		mu.Lock()
		defer mu.Unlock()
		if strings.Join(order, ",") != strings.Join(want, ",") {
			t.Fatalf("order = %v, want %v", order, want)
		}
	})
}

// storeOwningOAuthProvider is an extension OAuth provider that keeps its credentials in its own store (pig additive (D40)).
type storeOwningOAuthProvider struct {
	mu     sync.Mutex
	stored []ai.OAuthCredentials
}

func (*storeOwningOAuthProvider) ID() string               { return "anthropic" }
func (*storeOwningOAuthProvider) Name() string             { return "Extension Anthropic" }
func (*storeOwningOAuthProvider) UsesCallbackServer() bool { return false }
func (*storeOwningOAuthProvider) Login(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{Access: "dummy-access", Refresh: "dummy-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
}
func (*storeOwningOAuthProvider) RefreshToken(c ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return c, nil
}
func (*storeOwningOAuthProvider) GetAPIKey(c ai.OAuthCredentials) string { return c.Access }
func (p *storeOwningOAuthProvider) OAuthCredentialStatus() (ai.OAuthCredentialStatus, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ai.OAuthCredentialStatus{AuthType: "oauth", Source: "stored"}, len(p.stored) > 0
}
func (p *storeOwningOAuthProvider) StoreOAuthCredentials(c ai.OAuthCredentials) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stored = append(p.stored, c)
	return "extension store", nil
}
func (p *storeOwningOAuthProvider) DeleteOAuthCredentials() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	deleted := len(p.stored) > 0
	p.stored = nil
	return deleted, nil
}

// pig additive (D40): an extension OAuth provider that shadows a built-in ID and owns a credential store saves its login there, as /login (runLoginRegisteredOAuth) and pig login do; the core store stays untouched.
func TestModelRuntimeLoginSavesExtensionOAuthToItsCredentialStore(t *testing.T) {
	provider := &storeOwningOAuthProvider{}
	ai.RegisterOAuthProvider("anthropic", provider)
	t.Cleanup(func() { ai.UnregisterOAuthProvider("anthropic") })
	runtime, credentials := loginTestRuntime(t, "")
	if _, err := runtime.Login(t.Context(), "anthropic", ai.CredentialOAuth, ai.AuthInteraction{Notify: func(ai.AuthEvent) {}}); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	stored := slices.Clone(provider.stored)
	provider.mu.Unlock()
	if len(stored) != 1 || stored[0].Access != "dummy-access" || stored[0].Refresh != "dummy-refresh" {
		t.Fatalf("extension store = %+v, want the one login credential", stored)
	}
	if core, err := credentials.Read(t.Context(), "anthropic"); err != nil || core != nil {
		t.Fatalf("core store = %+v, %v; want untouched", core, err)
	}
}
