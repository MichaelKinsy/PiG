//go:build !pig_strip_node_extensions

package codingagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi composes a provider object's own auth (model-runtime.ts registerNativeProvider, composeProvider's base): its
// declared OAuth method is the provider's account login, labelled by its loginLabel (interactive-mode.ts:5893-5895), and
// it stays offered after the refresh every registration queues. A provider object without an OAuth method has no account
// login.
func TestNativeProviderLoginOffersDeclaredMethods(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		t.Run(language, func(t *testing.T) {
			m := newNativeLoginMode(t, language)
			check := func(stage string) {
				t.Helper()
				options := nativeLoginOptions(m, "sso-proxy")
				var kinds []string
				for _, option := range options {
					kinds = append(kinds, option.AuthType)
					if option.AuthType == "oauth" {
						oauth, ok := option.Method.(*ai.OAuthAuth)
						if !ok || oauth.LoginLabel != "Sign in with SSO Proxy" || oauth.Name != "SSO Proxy SSO" {
							t.Fatalf("%s: OAuth method = %#v", stage, option.Method)
						}
					}
					if option.AuthType == "api_key" {
						key, ok := option.Method.(*ai.APIKeyAuth)
						if !ok || key.Name != "SSO Proxy key" {
							t.Fatalf("%s: API-key method = %#v", stage, option.Method)
						}
					}
				}
				if slices.Sort(kinds); !slices.Equal(kinds, []string{"api_key", "oauth"}) {
					t.Fatalf("%s: sso-proxy login options = %v, want oauth and api_key", stage, kinds)
				}
				for _, option := range nativeLoginOptions(m, "plain-proxy") {
					if option.AuthType == "oauth" {
						t.Fatalf("%s: plain-proxy declares no OAuth method but offers an account login", stage)
					}
				}
			}
			check("registered")
			if err := m.opts.ModelRegistry.refreshNativeProvider(t.Context(), m.opts.ModelRegistry.NativeProvider("sso-proxy"), false, nil); err != nil {
				t.Fatal(err)
			}
			check("refreshed")

			done := dispatchLogin(t, m, "SSO Proxy")
			waitForLoopRender(t, m, m.editorContainer, "Select authentication method for SSO Proxy:")
			waitForLoopRender(t, m, m.editorContainer, "Sign in with SSO Proxy")
			if got := renderOnLoop(t, m, m.editorContainer); strings.Contains(got, "Sign in with an account") {
				t.Fatalf("the auth-type menu shows the generic account label instead of the flow's loginLabel:\n%s", got)
			}
			deliverModalInput(t, m, []byte("\x1b"))
			awaitLogin(t, done)
		})
	}
}

// Pi's showAuthPrompt opens a selector for a select prompt (interactive-mode.ts:6243-6276 showAuthSelect) and answers
// with the chosen option's id; cancelling the selector rejects with "Login cancelled", which ends the login and reopens
// the menu it was started from (interactive-mode.ts:6365-6366).
func TestNativeProviderOAuthLoginSelectPrompt(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		t.Run(language+"/choose", func(t *testing.T) {
			m := newNativeLoginMode(t, language)
			done := dispatchLogin(t, m, "SSO Proxy")
			waitForLoopRender(t, m, m.editorContainer, "Sign in with SSO Proxy")
			deliverModalInput(t, m, []byte("\r"))
			waitForLoopRender(t, m, m.editorContainer, "Choose a team:")
			waitForLoopRender(t, m, m.editorContainer, "Blue team")
			deliverModalInput(t, m, []byte("\x1b[B"))
			deliverModalInput(t, m, []byte("\r"))
			awaitLogin(t, done)
			credential, ok := storedNativeCredential(t, m, "sso-proxy")
			if !ok || credential.Type != ai.CredentialOAuth || credential.Access != "token-blue" {
				t.Fatalf("stored credential = %+v (present %v), want the blue team's OAuth token", credential, ok)
			}
			got := plainRender(m.chatContainer)
			if strings.Contains(got, "Login cancelled") || strings.Contains(got, "Failed") {
				t.Fatalf("a completed login reported a failure:\n%s", got)
			}
			// Pi names the login with the selected provider option's name, the provider object's own name (interactive-mode.ts:5877 showLoginDialog(providerOption.id, providerOption.name), :5802), not its OAuth method's name.
			if !strings.Contains(got, "Logged in to SSO Proxy") || strings.Contains(got, "SSO Proxy SSO") {
				t.Fatalf("the login is not named after the provider:\n%s", got)
			}
		})
		t.Run(language+"/cancel", func(t *testing.T) {
			m := newNativeLoginMode(t, language)
			done := dispatchLogin(t, m, "SSO Proxy")
			waitForLoopRender(t, m, m.editorContainer, "Sign in with SSO Proxy")
			deliverModalInput(t, m, []byte("\r"))
			waitForLoopRender(t, m, m.editorContainer, "Choose a team:")
			deliverModalInput(t, m, []byte("\x1b"))
			waitForLoopRender(t, m, m.editorContainer, "Select authentication method for SSO Proxy:")
			if got := renderOnLoop(t, m, m.chatContainer); strings.Contains(got, "Failed") {
				t.Fatalf("a cancelled select reported a failure:\n%s", got)
			}
			deliverModalInput(t, m, []byte("\x1b"))
			awaitLogin(t, done)
			if credential, ok := storedNativeCredential(t, m, "sso-proxy"); ok {
				t.Fatalf("a cancelled login stored %+v", credential)
			}
		})
	}
}

// Pi's showApiKeyLoginDialog runs the provider's own apiKey.login with the dialog's prompts and events
// (interactive-mode.ts:6192-6241, 6315-6336). An answered prompt stays in the dialog as "> value" (login-dialog.ts:80-84
// replaceInputWithSubmittedText), so the base URL is still shown while the key is asked, and the stored credential keeps
// both values.
func TestNativeProviderAPIKeyLoginRunsDeclaredLogin(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		t.Run(language, func(t *testing.T) {
			m := newNativeLoginMode(t, language)
			done := dispatchLogin(t, m, "SSO Proxy")
			waitForLoopRender(t, m, m.editorContainer, "Sign in with an API key")
			deliverModalInput(t, m, []byte("\x1b[B"))
			deliverModalInput(t, m, []byte("\r"))
			waitForLoopRender(t, m, m.editorContainer, "Proxy base URL:")
			waitForLoopRender(t, m, m.editorContainer, "e.g., https://proxy.example")
			deliverModalInput(t, m, []byte("https://sso.example/v1"))
			deliverModalInput(t, m, []byte("\r"))
			waitForLoopRender(t, m, m.editorContainer, "Proxy API key:")
			got := renderOnLoop(t, m, m.editorContainer)
			for _, want := range []string{"Proxy base URL:", "> https://sso.example/v1", "Proxy https://sso.example/v1 accepted"} {
				if !strings.Contains(got, want) {
					t.Fatalf("the dialog dropped %q while asking for the key:\n%s", want, got)
				}
			}
			if strings.Contains(got, "Enter API key") {
				t.Fatalf("the generic API-key prompt replaced the provider's own login:\n%s", got)
			}
			deliverModalInput(t, m, []byte("sk-proxy"))
			deliverModalInput(t, m, []byte("\r"))
			awaitLogin(t, done)
			credential, ok := storedNativeCredential(t, m, "sso-proxy")
			if !ok || credential.Type != ai.CredentialAPIKey || credential.Key != "sk-proxy" || credential.Env["SSO_PROXY_BASE_URL"] != "https://sso.example/v1" {
				t.Fatalf("stored credential = %+v (present %v), want the key and its base URL", credential, ok)
			}
		})
	}
}

// Without a declared apiKey.login the composed API-key method asks for the key with the generic secret prompt
// (provider-composer.ts composeApiKeyAuth: inherited?.login ?? the "Enter API key" prompt).
func TestNativeProviderAPIKeyLoginWithoutDeclaredLoginUsesGenericPrompt(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		t.Run(language, func(t *testing.T) {
			m := newNativeLoginMode(t, language)
			done := dispatchLogin(t, m, "Plain Proxy")
			waitForLoopRender(t, m, m.editorContainer, "Enter API key")
			deliverModalInput(t, m, []byte("plain-key"))
			deliverModalInput(t, m, []byte("\r"))
			awaitLogin(t, done)
			credential, ok := storedNativeCredential(t, m, "plain-proxy")
			if !ok || credential.Key != "plain-key" {
				t.Fatalf("stored credential = %+v (present %v)", credential, ok)
			}
		})
	}
}

// AuthPrompt and AuthEvent are closed unions (auth/types.ts). A prompt or event type outside them fails the login with
// an error naming the type; it is never answered as another type or dropped.
func TestNativeProviderLoginRejectsUnknownInteractionTypes(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		for _, tc := range []struct{ provider, name, kind string }{
			{"odd-proxy", "Odd Proxy", `"picture"`},
			{"loud-proxy", "Loud Proxy", `"fireworks"`},
		} {
			t.Run(language+"/"+tc.provider, func(t *testing.T) {
				m := newNativeLoginMode(t, language)
				done := dispatchLogin(t, m, tc.name)
				awaitLogin(t, done)
				got := plainRender(m.chatContainer)
				if !strings.Contains(got, "Failed to save API key for "+tc.name) || !strings.Contains(got, tc.kind) {
					t.Fatalf("an unknown interaction type was not reported as a login failure:\n%s", got)
				}
				if credential, ok := storedNativeCredential(t, m, tc.provider); ok {
					t.Fatalf("a failed login stored %+v", credential)
				}
			})
		}
	}
}
