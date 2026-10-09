package mcpext

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// String.prototype.trim removes U+FEFF and keeps U+0085; Go's strings.TrimSpace does the reverse. These cases tell the
// two apart at every trim of oauth.ts.

// oauth.ts parseStates: `if (!content?.trim()) return {}`.
func TestParseStatesTreatsJavaScriptWhitespaceAsEmpty(t *testing.T) {
	states, err := parseStates("\uFEFF\n\u3000")
	if err != nil || states.Len() != 0 {
		t.Fatalf("parseStates(BOM) = %v, %v; want an empty object", states, err)
	}
	if _, err := parseStates("\u0085"); err == nil {
		t.Fatal("parseStates(U+0085) succeeded; JSON.parse of a non-whitespace character fails")
	}
}

// oauth.ts responseFromRedirectUrl: `new URL(input.trim())`. A BOM around the pasted URL is removed; U+0085 stays and
// makes the URL invalid (node: `new URL("\u0085http://…")` throws "Invalid URL").
func TestResponseFromRedirectURLTrimsLikeJavaScript(t *testing.T) {
	redirect, _ := url.Parse("http://127.0.0.1:1/callback")
	response, err := responseFromRedirectURL("\uFEFFhttp://127.0.0.1:1/callback?code=c&state=s\uFEFF", "s", redirect)
	if err != nil || response.code != "c" {
		t.Fatalf("BOM-padded URL: %+v, %v", response, err)
	}
	if _, err := responseFromRedirectURL("\u0085http://127.0.0.1:1/callback?code=c&state=s", "s", redirect); err == nil || err.Error() != "Expected the full redirect URL from the browser address bar" {
		t.Fatalf("U+0085-prefixed URL: err = %v", err)
	}
}

type fixedPrompt string

func (fixedPrompt) ShowAuthorizationURL(*url.URL) {}

func (p fixedPrompt) PromptForRedirectURL(context.Context) (string, error) { return string(p), nil }

// oauth.ts waitForAuthorizationResponse: `if (!input?.trim()) throw new McpSignInCancelledError()`.
func TestWaitForAuthorizationResponseCancelsOnAJavaScriptBlankAnswer(t *testing.T) {
	callback, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = callback.Close() })
	redirect, _ := url.Parse(callback.RedirectURL)
	authorization, _ := url.Parse("https://auth.example/authorize")
	_, err = waitForAuthorizationResponse(t.Context(), callback, authorization, redirect, "s", fixedPrompt("\uFEFF \u2028"))
	if cancelled := (*McpSignInCancelledError)(nil); !errors.As(err, &cancelled) {
		t.Fatalf("BOM answer: err = %v, want the sign-in cancelled", err)
	}
	_, err = waitForAuthorizationResponse(t.Context(), callback, authorization, redirect, "t", fixedPrompt("\u0085"))
	if cancelled := (*McpSignInCancelledError)(nil); errors.As(err, &cancelled) || err == nil {
		t.Fatalf("U+0085 answer: err = %v, want the redirect URL rejected", err)
	}
}

// showingPrompt follows the redirect as soon as the authorization URL is shown, like a browser that approves at once.
type showingPrompt struct {
	t        *testing.T
	redirect string
}

func (p showingPrompt) ShowAuthorizationURL(*url.URL) {
	response, err := http.Get(p.redirect + "?state=s&code=the-code&iss=https%3A%2F%2Fas.example") //nolint:noctx // loopback test server
	if err != nil {
		p.t.Error(err)
		return
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		p.t.Errorf("redirect status = %d: the state was not pending when the URL was shown", response.StatusCode)
	}
}

func (showingPrompt) PromptForRedirectURL(ctx context.Context) (string, error) {
	<-ctx.Done()
	return "", nil
}

// oauth.ts waitForAuthorizationResponse: upstream registers waitForCallback in the same turn that shows the URL, so a browser that follows the redirect at once finds the state pending.
func TestWaitForAuthorizationResponseRegistersTheStateBeforeShowingTheURL(t *testing.T) {
	callback, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = callback.Close() })
	redirect, _ := url.Parse(callback.RedirectURL)
	authorization, _ := url.Parse("https://auth.example/authorize")
	response, err := waitForAuthorizationResponse(t.Context(), callback, authorization, redirect, "s", showingPrompt{t: t, redirect: callback.RedirectURL})
	if err != nil || response.code != "the-code" || response.iss == nil || *response.iss != "https://as.example" {
		t.Fatalf("response = %+v, %v", response, err)
	}
}
