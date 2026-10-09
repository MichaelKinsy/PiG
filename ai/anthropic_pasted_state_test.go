package ai

// pi: packages/ai/src/auth/oauth/anthropic.ts

import (
	"net/http"
	"testing"
)

// Pi's parseAuthorizationInput leaves state undefined only when the paste names none: `"code#".split("#", 2)` and a `state=` parameter give an
// empty state. Both logins then send `parsed.state ?? verifier` (anthropic.ts:189 and :230), so an empty pasted state reaches the token request
// as "", and only an absent one becomes the PKCE verifier. The empty state passes the mismatch check, which tests truthiness (anthropic.ts:187, :225).
func TestAnthropicLoginSendsAnEmptyPastedStateAsPiDoes(t *testing.T) {
	cases := []struct {
		name, method, paste string
		wantVerifier        bool
	}{
		{name: "copy code with an empty state after #", method: AnthropicCopyCodeLoginMethod, paste: "copied-code#"},
		{name: "copy code with an empty state parameter", method: AnthropicCopyCodeLoginMethod, paste: "code=copied-code&state="},
		{name: "browser paste of a redirect with an empty state", method: AnthropicBrowserLoginMethod, paste: "http://localhost:53692/callback?code=copied-code&state="},
		{name: "copy code without a state", method: AnthropicCopyCodeLoginMethod, paste: "copied-code", wantVerifier: true},
		{name: "browser paste of a redirect without a state", method: AnthropicBrowserLoginMethod, paste: "http://localhost:53692/callback?code=copied-code", wantVerifier: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]string
			calls := mockAnthropicOAuthToken(t, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`, func(_ *http.Request, body map[string]string) { sent = body })
			_, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{
				OnAuth:            func(OAuthAuthInfo) {},
				OnSelect:          func(OAuthSelectPrompt) (string, error) { return tc.method, nil },
				OnManualCodeInput: func() (string, error) { return tc.paste, nil },
			})
			if err != nil || *calls != 1 {
				t.Fatalf("err=%v calls=%d", err, *calls)
			}
			if sent["code"] != "copied-code" || sent["code_verifier"] == "" {
				t.Fatalf("body=%v", sent)
			}
			if wantState := map[bool]string{true: sent["code_verifier"]}[tc.wantVerifier]; sent["state"] != wantState {
				t.Fatalf("state=%q want %q (verifier %q)", sent["state"], wantState, sent["code_verifier"])
			}
		})
	}
}
