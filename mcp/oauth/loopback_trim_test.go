package oauth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// loopback.ts waitForLoopbackSignIn: `if (!input?.trim()) throw new McpSignInCancelledError()`. String.prototype.trim removes U+FEFF and keeps U+0085.
func TestWaitForLoopbackSignInCancelsOnAJavaScriptBlankAnswer(t *testing.T) {
	loopback := listenAt(t, "https://mcp.example/mcp", false)
	answer := func(text string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return text, nil }
	}
	_, err := oauth.WaitForLoopbackSignIn(t.Context(), oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: pendingRedirect(loopback.RedirectURL), PromptForRedirectURL: answer("\uFEFF \u2028"),
	})
	expectCancelled(t, err)
	second := pendingRedirect(loopback.RedirectURL)
	second.State = "t"
	_, err = oauth.WaitForLoopbackSignIn(t.Context(), oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: second, PromptForRedirectURL: answer("\u0085"),
	})
	if _, cancelled := errors.AsType[*oauth.McpSignInCancelledError](err); cancelled || err == nil {
		t.Fatalf("U+0085 answer: err = %v, want the redirect URL rejected", err)
	}
}

// loopback.ts: upstream registers waitForCallback in the same turn that returns the promise, so a browser that follows the redirect once the
// caller shows the URL finds the state pending. OnRegistered is the Go form of that turn: the browser follows the redirect from it.
func TestWaitForLoopbackSignInRegistersTheStateBeforeOnRegistered(t *testing.T) {
	loopback := listenAt(t, "https://mcp.example/mcp", false)
	response, err := oauth.WaitForLoopbackSignIn(t.Context(), oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: pendingRedirect(loopback.RedirectURL),
		OnRegistered: func() {
			got, err := http.Get(loopback.RedirectURL + "?state=state-1&code=the-code&iss=https%3A%2F%2Fas.example") //nolint:noctx // loopback test server
			if err != nil {
				t.Error(err)
				return
			}
			_ = got.Body.Close()
			if got.StatusCode != http.StatusOK {
				t.Errorf("redirect status = %d: the state was not pending", got.StatusCode)
			}
		},
		PromptForRedirectURL: func(ctx context.Context) (string, error) { <-ctx.Done(); return "", nil },
	})
	if err != nil || response.Code != "the-code" || response.Iss != "https://as.example" {
		t.Fatalf("response = %+v, %v", response, err)
	}
}
