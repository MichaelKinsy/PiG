package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// auth/types.ts: AuthPrompt and AuthEvent are closed unions discriminated by type. Each variant decodes to its Go
// variant; any other type, or a callback other than prompt and notify, is an error.
func TestDecodeNativeLoginCallback(t *testing.T) {
	interval := 5.0
	cases := []struct {
		name, data string
		want       any
		err        string
	}{
		{"text", `{"method":"prompt","params":{"prompt":{"type":"text","message":"URL","placeholder":"https://x"}}}`, ai.AuthTextPrompt{Message: "URL", Placeholder: "https://x"}, ""},
		{"secret", `{"method":"prompt","params":{"prompt":{"type":"secret","message":"Key"}}}`, ai.AuthSecretPrompt{Message: "Key"}, ""},
		{"manual code", `{"method":"prompt","params":{"prompt":{"type":"manual_code","message":"Code"}}}`, ai.AuthManualCodePrompt{Message: "Code"}, ""},
		{"select", `{"method":"prompt","params":{"prompt":{"type":"select","message":"Team","options":[{"id":"a","label":"A","description":"first"}]}}}`, ai.AuthSelectPrompt{Message: "Team", Options: []ai.AuthSelectOption{{ID: "a", Label: "A", Description: "first"}}}, ""},
		{"info", `{"method":"notify","params":{"event":{"type":"info","message":"Note","links":[{"url":"https://x","label":"X"}]}}}`, ai.AuthInfoEvent{Message: "Note", Links: []ai.AuthInfoLink{{URL: "https://x", Label: "X"}}}, ""},
		{"auth url", `{"method":"notify","params":{"event":{"type":"auth_url","url":"https://x","instructions":"Go"}}}`, ai.AuthURLEvent{URL: "https://x", Instructions: "Go"}, ""},
		{"device code", `{"method":"notify","params":{"event":{"type":"device_code","userCode":"AB","verificationUri":"https://x","intervalSeconds":5}}}`, ai.AuthDeviceCodeEvent{UserCode: "AB", VerificationURI: "https://x", IntervalSeconds: &interval}, ""},
		{"progress", `{"method":"notify","params":{"event":{"type":"progress","message":"Busy"}}}`, ai.AuthProgressEvent{Message: "Busy"}, ""},
		{"unknown prompt", `{"method":"prompt","params":{"prompt":{"type":"picture","message":"Draw"}}}`, nil, `unknown login prompt type "picture"`},
		{"untyped prompt", `{"method":"prompt","params":{"prompt":{"message":"Draw"}}}`, nil, `unknown login prompt type ""`},
		{"unknown event", `{"method":"notify","params":{"event":{"type":"fireworks"}}}`, nil, `unknown login event type "fireworks"`},
		{"unknown method", `{"method":"dance","params":{}}`, nil, "unexpected login callback dance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			callback, err := decodeNativeLoginCallback(json.RawMessage(tc.data))
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got any = callback.prompt
			if callback.event != nil {
				got = callback.event
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("decoded %#v, want %#v", got, tc.want)
			}
		})
	}
}

// A prompt's answer is the entered or selected string, a rejected prompt rejects the reverse call with the same error,
// and a notification is acknowledged with null.
func TestNativeAuthInteractionAnswersTheLoginDialog(t *testing.T) {
	cancelled := errors.New("Login cancelled")
	var events []ai.AuthEvent
	callback := nativeAuthInteraction(t.Context(), ai.AuthInteraction{
		Prompt: func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
			if selected, ok := prompt.(ai.AuthSelectPrompt); ok {
				return selected.Options[1].ID, nil
			}
			return "", cancelled
		},
		Notify: func(event ai.AuthEvent) { events = append(events, event) },
	})
	answer, err := callback(json.RawMessage(`{"method":"prompt","params":{"prompt":{"type":"select","message":"Team","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}]}}}`))
	if err != nil || string(answer) != `"b"` {
		t.Fatalf("select answered %s, %v", answer, err)
	}
	if _, err := callback(json.RawMessage(`{"method":"prompt","params":{"prompt":{"type":"text","message":"URL"}}}`)); !errors.Is(err, cancelled) {
		t.Fatalf("a rejected prompt returned %v", err)
	}
	answer, err = callback(json.RawMessage(`{"method":"notify","params":{"event":{"type":"progress","message":"Busy"}}}`))
	if err != nil || string(answer) != "null" || !reflect.DeepEqual(events, []ai.AuthEvent{ai.AuthProgressEvent{Message: "Busy"}}) {
		t.Fatalf("notify answered %s, %v; events %#v", answer, err, events)
	}
	if _, err := callback(json.RawMessage(`{"method":"notify","params":{"event":{"type":"fireworks"}}}`)); err == nil || !strings.Contains(err.Error(), `"fireworks"`) {
		t.Fatalf("an unknown event returned %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("an unknown event reached the dialog: %#v", events)
	}
}

// auth/types.ts ApiKeyAuth.login is optional (absent = ambient-only) and OAuthAuth.loginLabel names the account sign-in.
// The carrier carries a login exactly when the provider object declares one, and the declared loginLabel.
func TestNativeProviderCarrierCarriesDeclaredLogins(t *testing.T) {
	label := "Sign in with Proxy"
	for _, tc := range []struct {
		name    string
		methods []string
		label   *string
	}{
		{"declared", []string{"auth.apiKey.resolve", "auth.apiKey.login", "auth.oauth.login", "auth.oauth.refresh", "auth.oauth.toAuth"}, &label},
		{"undeclared", []string{"auth.apiKey.resolve", "auth.oauth.login", "auth.oauth.refresh", "auth.oauth.toAuth"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := &nativeProviderProxy{declaration: NativeProviderDeclaration{ID: "p", Name: "P", Methods: tc.methods, Auth: &ProviderObjectAuthDeclaration{
				APIKey: &ProviderObjectAuthMethodDeclaration{Name: "Key"},
				OAuth:  &ProviderObjectAuthMethodDeclaration{Name: "Account", LoginLabel: tc.label},
			}}}
			auth := proxy.carrier().Auth
			if declared := tc.label != nil; (auth.APIKey.Login != nil) != declared {
				t.Fatalf("API-key login present = %v, want %v", auth.APIKey.Login != nil, declared)
			}
			if auth.OAuth.Login == nil {
				t.Fatal("the OAuth method has no login")
			}
			want := ""
			if tc.label != nil {
				want = *tc.label
			}
			if auth.OAuth.LoginLabel != want {
				t.Fatalf("loginLabel = %q, want %q", auth.OAuth.LoginLabel, want)
			}
		})
	}
}
