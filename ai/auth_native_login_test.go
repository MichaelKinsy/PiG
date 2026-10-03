package ai

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestModelsLoginUsesNativeEnvKeyPrompt(t *testing.T) {
	credentials := NewInMemoryCredentialStore()
	models := CreateModels(CreateModelsOptions{Credentials: credentials})
	models.SetProvider(&ModelsProvider{ID: "key", Name: "Key", Auth: ProviderAuth{APIKey: EnvAPIKeyAuth("Test API key", "TEST_KEY")}})
	var prompt AuthPrompt
	credential, err := models.Login(t.Context(), "key", CredentialAPIKey, AuthInteraction{Prompt: func(ctx context.Context, value AuthPrompt) (string, error) {
		if ctx != t.Context() {
			t.Error("login replaced caller context")
		}
		prompt = value
		return "literal-key", nil
	}, Notify: func(AuthEvent) {}})
	if err != nil || credential.Key != "literal-key" || !reflect.DeepEqual(prompt, AuthSecretPrompt{Message: "Enter Test API key"}) {
		t.Fatalf("credential=%v prompt=%v error=%v", credential, prompt, err)
	}
	stored, err := credentials.Read(t.Context(), "key")
	if err != nil || stored.Key != "literal-key" {
		t.Fatalf("stored=%v err=%v", stored, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err = models.Login(ctx, "key", CredentialAPIKey, AuthInteraction{Prompt: func(context.Context, AuthPrompt) (string, error) { cancel(); return "must-not-save", nil }, Notify: func(AuthEvent) {}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	models.operations.Wait()
	credentials.operations.Wait()
	stored, err = credentials.Read(t.Context(), "key")
	if err != nil || stored.Key != "literal-key" {
		t.Fatalf("cancel wrote credential=%v err=%v", stored, err)
	}
}

type nativeLoginOAuth struct {
	run func(context.Context, OAuthLoginCallbacks) (OAuthCredentials, error)
}

func (nativeLoginOAuth) ID() string               { return "openrouter" }
func (nativeLoginOAuth) Name() string             { return "OpenRouter" }
func (nativeLoginOAuth) UsesCallbackServer() bool { return true }
func (p nativeLoginOAuth) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return p.run(context.Background(), callbacks)
}
func (p nativeLoginOAuth) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return p.run(ctx, callbacks)
}
func (nativeLoginOAuth) RefreshToken(value OAuthCredentials) (OAuthCredentials, error) {
	return value, nil
}
func (nativeLoginOAuth) GetAPIKey(value OAuthCredentials) string { return value.Access }

func TestNativeOAuthLoginPreservesPromptContextAndCallbackURL(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	promptCtx, promptCancel := context.WithCancel(ctx)
	defer promptCancel()
	provider := nativeLoginOAuth{run: func(received context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
		if received != ctx {
			t.Error("native login changed context")
		}
		callbacks.OnAuth(OAuthAuthInfo{URL: "https://openrouter.ai/auth?callback_url=http%3A%2F%2Flocalhost%3A4321%2Fcallback", Instructions: "browser"})
		code, err := callbacks.OnManualCodeInputContext(promptCtx)
		if err != nil {
			return OAuthCredentials{}, err
		}
		return OAuthCredentials{Access: code, Refresh: "refresh", Expires: 100, Scope: "profile"}, nil
	}}
	var notifications []AuthEvent
	credential, err := oauthNativeLogin(provider)(ctx, AuthInteraction{Prompt: func(received context.Context, prompt AuthPrompt) (string, error) {
		if received != promptCtx || !reflect.DeepEqual(prompt, AuthManualCodePrompt{Message: "Complete sign-in in your browser, or paste the authorization code / redirect URL here:", Placeholder: "http://localhost:4321/callback"}) {
			t.Errorf("prompt=%+v context=%v", prompt, received)
		}
		return "code", nil
	}, Notify: func(event AuthEvent) { notifications = append(notifications, event) }}, LoginOptions{})
	if err != nil || credential.Type != CredentialOAuth || credential.Access != "code" || credential.Refresh != "refresh" || credential.Expires != 100 || credential.Scope != "profile" || len(notifications) != 1 {
		t.Fatalf("credential=%v events=%v err=%v", credential, notifications, err)
	}
}
