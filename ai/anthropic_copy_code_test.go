package ai

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

// Pi 1.0.0 anthropic.ts:191-226 runs copy code login through the interaction it was given: the select prompt names the
// methods, and the manual_code prompt carries the flow's own message and placeholder to the UI
// (interactive-mode.ts:6207-6208 shows prompt.message).
func TestAnthropicCopyCodeLoginPromptsThroughTheModelRuntime(t *testing.T) {
	var authState string
	calls := mockAnthropicOAuthToken(t, `{"access_token":"copied-access","refresh_token":"copied-refresh","expires_in":3600}`, func(_ *http.Request, body map[string]string) {
		if body["code"] != "copied-code" || body["state"] != authState || body["redirect_uri"] != "https://platform.claude.com/oauth/code/callback" {
			t.Errorf("body=%v", body)
		}
	})
	store := NewInMemoryCredentialStore()
	models := CreateModels(CreateModelsOptions{Credentials: store})
	auth, err := BuiltinProviderAuth("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	models.SetProvider(&ModelsProvider{ID: "anthropic", Name: "Anthropic", Auth: auth})
	var prompts []AuthPrompt
	var events []AuthEvent
	credential, err := models.Login(t.Context(), "anthropic", CredentialOAuth, AuthInteraction{
		Notify: func(event AuthEvent) {
			events = append(events, event)
			if event, ok := event.(AuthURLEvent); ok {
				parsed, err := url.Parse(event.URL)
				if err != nil {
					t.Error(err)
					return
				}
				authState = parsed.Query().Get("state")
			}
		},
		Prompt: func(_ context.Context, prompt AuthPrompt) (string, error) {
			prompts = append(prompts, prompt)
			if _, ok := prompt.(AuthSelectPrompt); ok {
				return AnthropicCopyCodeLoginMethod, nil
			}
			return "copied-code#" + authState, nil
		},
	})
	models.operations.Wait()
	store.operations.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if credential.Access != "copied-access" || *calls != 1 {
		t.Fatalf("credential=%+v calls=%d", credential, *calls)
	}
	want := []AuthPrompt{
		AuthSelectPrompt{Message: "Select Anthropic login method:", Options: []AuthSelectOption{{ID: "browser", Label: "Browser login (default)"}, {ID: "copy_code", Label: "Copy code login (headless)"}}},
		AuthManualCodePrompt{Message: "Paste the code Anthropic shows after you sign in:", Placeholder: "code#state"},
	}
	if !reflect.DeepEqual(prompts, want) {
		t.Fatalf("prompts=%#v\nwant %#v", prompts, want)
	}
	if len(events) == 0 {
		t.Fatal("no auth events")
	}
	if event, ok := events[0].(AuthURLEvent); !ok || event.Instructions != "Complete login in your browser, then copy the code Anthropic shows and paste it here." {
		t.Fatalf("first event=%#v", events[0])
	}
}

// Pi 1.0.0 anthropic.ts:213-215 rejects a pasted state that is not the login's before any token request.
func TestAnthropicCopyCodeLoginRejectsForeignState(t *testing.T) {
	calls := mockAnthropicOAuthToken(t, `{}`, func(*http.Request, map[string]string) {})
	_, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{
		OnSelect:          func(OAuthSelectPrompt) (string, error) { return AnthropicCopyCodeLoginMethod, nil },
		OnManualCodeInput: func() (string, error) { return "code#not-this-login", nil },
	})
	if err == nil || err.Error() != "OAuth state mismatch" || *calls != 0 {
		t.Fatalf("err=%v calls=%d", err, *calls)
	}
	_, err = (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{
		OnSelect: func(OAuthSelectPrompt) (string, error) { return "sideways", nil },
	})
	if err == nil || err.Error() != "Unknown Anthropic login method: sideways" {
		t.Fatalf("unknown method err=%v", err)
	}
	cancelled := errors.New("Login cancelled")
	_, err = (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{
		OnSelect:                 func(OAuthSelectPrompt) (string, error) { return AnthropicCopyCodeLoginMethod, nil },
		OnManualCodeInputContext: func(context.Context) (string, error) { return "", cancelled },
	})
	if !errors.Is(err, cancelled) {
		t.Fatalf("cancelled paste err=%v", err)
	}
}
