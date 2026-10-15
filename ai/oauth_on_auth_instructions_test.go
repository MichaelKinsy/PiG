package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The OnAuth callback of OAuthLoginCallbacks is Pi's auth-url interaction event (auth/types.ts AuthUrlEvent {url, instructions}). Each login flow reports its
// authorize URL together with its own instructions text, exactly once, before it asks for the pasted code:
// auth/oauth/anthropic.ts:174 (callback flow) and :215 (copy-code flow), openai-chatgpt.ts:268, openai-codex.ts:378 and openrouter.ts:142.
func TestOAuthLoginReportsItsAuthorizeURLAndPisInstructions(t *testing.T) {
	stop := errors.New("stop after the authorize URL")
	cases := []struct {
		name         string
		login        func(context.Context, OAuthLoginCallbacks) (OAuthCredentials, error)
		urlPrefix    string
		instructions string
	}{
		{"anthropic callback", LoginAnthropic, "https://claude.ai/oauth/authorize?", "Complete login in your browser. If the browser is on another machine, paste the final redirect URL here."},
		{"anthropic copy code", LoginAnthropicCopyCode, "https://claude.ai/oauth/authorize?", "Complete login in your browser, then copy the code Anthropic shows and paste it here."},
		{"openai chatgpt", LoginOpenAIChatGPT, chatgptAuthorizeURL + "?", "Complete sign-in in your browser. If the callback does not complete, paste the final redirect URL here."},
		{"openai codex", LoginOpenAICodex, codexAuthorizeURL + "?", "A browser window should open. Complete login to finish."},
		{"openrouter", LoginOpenRouter, openRouterAuthorizeURL + "?", "Complete sign-in in your browser. If the browser is on another machine, paste the final redirect URL here."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_OAUTH_CALLBACK_HOST", "")
			var infos []OAuthAuthInfo
			_, err := tc.login(t.Context(), OAuthLoginCallbacks{
				OnAuth:                   func(info OAuthAuthInfo) { infos = append(infos, info) },
				GetDeviceID:              func() string { return "11111111-1111-4111-8111-111111111111" },
				OnManualCodeInput:        func() (string, error) { return "", stop },
				OnManualCodeInputContext: func(context.Context) (string, error) { return "", stop },
			})
			if !errors.Is(err, stop) {
				t.Fatalf("login error = %v, want the manual prompt error", err)
			}
			if len(infos) != 1 || infos[0].Instructions != tc.instructions || !strings.HasPrefix(infos[0].URL, tc.urlPrefix) {
				t.Fatalf("OnAuth reported %+v, want one event with the instructions %q and a URL starting %q", infos, tc.instructions, tc.urlPrefix)
			}
		})
	}
}
