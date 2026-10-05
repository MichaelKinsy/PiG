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

// model-runtime.ts:817-829 serializes same-provider credential operations through enqueueCredentialOperation. A second login on a built-in provider starts only after the first one finishes.
func TestModelRuntimeLoginSerializesBuiltInProvider(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime, _ := loginTestRuntime(t, "")
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
			_, errs[0] = runtime.Login(t.Context(), "anthropic", ai.CredentialAPIKey, prompt("first", true))
		})
		synctest.Wait()
		wg.Go(func() {
			_, errs[1] = runtime.Login(t.Context(), "anthropic", ai.CredentialAPIKey, prompt("second", false))
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
