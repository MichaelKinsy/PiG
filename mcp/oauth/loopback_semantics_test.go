package oauth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/mcp/src/oauth/loopback.ts (responseFromRedirectUrl, waitForLoopbackSignIn).
//
// pig mapping: String.prototype.trim removes U+FEFF and keeps U+0085; Go's strings.TrimSpace does the reverse. These
// cases tell the two apart at the trims of loopback.ts.

func redirectAt(redirectURL string) oauth.McpSignInRedirect {
	return oauth.McpSignInRedirect{State: "s", RedirectURL: redirectURL}
}

// loopback.ts responseFromRedirectUrl: `new URL(input.trim())`. A BOM around the pasted URL is removed; U+0085 stays
// and makes the URL invalid (node: `new URL("\u0085http://…")` throws "Invalid URL").
func TestResponseFromRedirectURLTrimsLikeJavaScript(t *testing.T) {
	redirect := redirectAt("http://127.0.0.1:1/callback")
	response, err := oauth.ResponseFromRedirectURL("\uFEFFhttp://127.0.0.1:1/callback?code=c&state=s\uFEFF", redirect)
	if err != nil || response.Code != "c" {
		t.Fatalf("BOM-padded URL: %+v, %v", response, err)
	}
	if _, err := oauth.ResponseFromRedirectURL("\u0085http://127.0.0.1:1/callback?code=c&state=s", redirect); err == nil || err.Error() != "Expected the full redirect URL from the browser address bar" {
		t.Fatalf("U+0085-prefixed URL: err = %v", err)
	}
}

// loopback.ts responseFromRedirectUrl: a pasted redirect URL carries its `iss` parameter to the code exchange, and
// `...(iss ? { iss } : {})` drops an empty one, like a response from the callback server.
func TestResponseFromRedirectURLKeepsANonEmptyIssParameter(t *testing.T) {
	for _, c := range []struct{ query, iss string }{
		{"code=c&state=s", ""},
		{"code=c&state=s&iss=https%3A%2F%2Fas.example", "https://as.example"},
		{"code=c&state=s&iss=", ""},
	} {
		response, err := oauth.ResponseFromRedirectURL("http://127.0.0.1:1/callback?"+c.query, redirectAt("http://127.0.0.1:1/callback"))
		if err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if response.Code != "c" || response.State != "s" || response.Iss != c.iss {
			t.Fatalf("%s: response = %+v, want iss %q", c.query, response, c.iss)
		}
	}
}

// loopback.ts responseFromRedirectUrl: each failure has its own message, checked in this order: unparsable input, origin
// or path mismatch, provider error (error_description first), wrong state, missing code; a code returns with its
// optional iss.
func TestResponseFromRedirectURLMessagesAndOrder(t *testing.T) {
	redirect := redirectAt("http://127.0.0.1:1/callback")
	for _, tc := range []struct {
		name, input, wantErr, wantCode, wantISS string
	}{
		{"not a URL", "callback?code=c", "Expected the full redirect URL from the browser address bar", "", ""},
		{"other origin", "http://127.0.0.1:2/callback?code=c&state=s", "The redirect URL does not match this sign-in's redirect URI", "", ""},
		{"other path", "http://127.0.0.1:1/other?code=c&state=s", "The redirect URL does not match this sign-in's redirect URI", "", ""},
		{"mismatch wins over a provider error", "http://127.0.0.1:1/other?error=denied", "The redirect URL does not match this sign-in's redirect URI", "", ""},
		{"provider error text", "http://127.0.0.1:1/callback?error=access_denied&state=other", "access_denied", "", ""},
		{"error description wins", "http://127.0.0.1:1/callback?error=access_denied&error_description=User+said+no", "User said no", "", ""},
		{"empty error description is still the description", "http://127.0.0.1:1/callback?error=access_denied&error_description=", "", "", ""},
		{"wrong state", "http://127.0.0.1:1/callback?code=c&state=other", "The redirect URL belongs to a different sign-in", "", ""},
		{"missing state", "http://127.0.0.1:1/callback?code=c", "The redirect URL belongs to a different sign-in", "", ""},
		{"state wins over a missing code", "http://127.0.0.1:1/callback?state=other", "The redirect URL belongs to a different sign-in", "", ""},
		{"missing code", "http://127.0.0.1:1/callback?state=s", "The redirect URL does not contain an authorization code", "", ""},
		{"empty code", "http://127.0.0.1:1/callback?state=s&code=", "The redirect URL does not contain an authorization code", "", ""},
		{"code without iss", "  http://127.0.0.1:1/callback?state=s&code=abc  ", "", "abc", ""},
		{"code with iss", "http://127.0.0.1:1/callback?state=s&code=abc&iss=https%3A%2F%2Fas.example", "", "abc", "https://as.example"},
		{"empty iss is dropped", "http://127.0.0.1:1/callback?state=s&code=abc&iss=", "", "abc", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := oauth.ResponseFromRedirectURL(tc.input, redirect)
			if tc.wantErr != "" || tc.name == "empty error description is still the description" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || response.Code != tc.wantCode || response.Iss != tc.wantISS {
				t.Fatalf("response = %+v, %v, want code %q and iss %q", response, err, tc.wantCode, tc.wantISS)
			}
		})
	}
}

// loopback.ts waitForLoopbackSignIn: `if (!input?.trim()) throw new McpSignInCancelledError()`.
func TestWaitForLoopbackSignInCancelsOnAJavaScriptBlankAnswer(t *testing.T) {
	loopback := listenAt(t, "https://mcp.example/mcp", false)
	prompt := func(answer string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return answer, nil }
	}
	authorization := redirectAt(loopback.RedirectURL)
	_, err := oauth.WaitForLoopbackSignIn(t.Context(), oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: authorization, PromptForRedirectURL: prompt("\uFEFF \u2028"),
	})
	expectCancelled(t, err)
	authorization.State = "t"
	_, err = oauth.WaitForLoopbackSignIn(t.Context(), oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: authorization, PromptForRedirectURL: prompt("\u0085"),
	})
	if _, cancelled := errors.AsType[*oauth.McpSignInCancelledError](err); cancelled || err == nil {
		t.Fatalf("U+0085 answer: err = %v, want the redirect URL rejected", err)
	}
}

// loopback.ts waitForLoopbackSignIn: upstream registers waitForCallback in the same turn that its caller shows the URL,
// so a browser that follows the redirect at once finds the state pending. OnPending is that turn.
func TestWaitForLoopbackSignInRegistersTheStateBeforeOnPending(t *testing.T) {
	loopback := listenAt(t, "https://mcp.example/mcp", false)
	response, err := oauth.WaitForLoopbackSignIn(t.Context(), oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: redirectAt(loopback.RedirectURL),
		OnPending: func() {
			// The browser approves at once.
			reply, err := http.Get(loopback.RedirectURL + "?state=s&code=the-code&iss=https%3A%2F%2Fas.example") //nolint:noctx // loopback test server
			if err != nil {
				t.Error(err)
				return
			}
			_ = reply.Body.Close()
			if reply.StatusCode != http.StatusOK {
				t.Errorf("redirect status = %d: the state was not pending when OnPending ran", reply.StatusCode)
			}
		},
		PromptForRedirectURL: func(ctx context.Context) (string, error) { <-ctx.Done(); return "", nil },
	})
	if err != nil || response.Code != "the-code" || response.Iss != "https://as.example" {
		t.Fatalf("response = %+v, %v", response, err)
	}
}
