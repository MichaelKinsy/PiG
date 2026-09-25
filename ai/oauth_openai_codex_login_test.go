package ai

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type codexRoundTrip func(*http.Request) (*http.Response, error)

func (f codexRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// holdCodexCallbackPort makes the callback server's bind fail, as when another
// login or process already owns port 1455.
func holdCodexCallbackPort(t *testing.T) {
	t.Helper()
	t.Setenv("PI_OAUTH_CALLBACK_HOST", "127.0.0.1")
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", codexCallbackPort))
	if err != nil {
		return // already held by another process: the bind fails either way
	}
	t.Cleanup(func() { _ = ln.Close() })
}

// stubCodexTokenEndpoint answers the authorization-code exchange and records
// the submitted code.
func stubCodexTokenEndpoint(t *testing.T) *string {
	t.Helper()
	var gotCode string
	prev := http.DefaultClient.Transport
	http.DefaultClient.Transport = codexRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != codexTokenURL {
			return nil, fmt.Errorf("unexpected request %s", r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		gotCode = form.Get("code")
		jwt := "h." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct_1"}}`)) + ".s"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"access_token":%q,"refresh_token":"r","expires_in":3600}`, jwt))),
		}, nil
	})
	t.Cleanup(func() { http.DefaultClient.Transport = prev })
	return &gotCode
}

// Upstream loginOpenAICodex always prompts for a manual code. When the
// callback server cannot bind, waitForCode resolves null at once and the
// pasted code completes the login. The browser flow notifies no progress: it
// reports neither the failed bind nor the code exchange.
func TestLoginOpenAICodexUsesManualCodeWhenCallbackPortBusy(t *testing.T) {
	holdCodexCallbackPort(t)
	gotCode := stubCodexTokenEndpoint(t)
	var progress []string
	creds, err := LoginOpenAICodex(context.Background(), OAuthLoginCallbacks{
		OnAuth:            func(OAuthAuthInfo) {},
		OnManualCodeInput: func() (string, error) { return "manual-code", nil },
		OnProgress:        func(m string) { progress = append(progress, m) },
	})
	if err != nil {
		t.Fatalf("login with busy callback port: %v", err)
	}
	if *gotCode != "manual-code" || creds.Refresh != "r" {
		t.Fatalf("exchanged code %q, refresh %q; want manual-code, r", *gotCode, creds.Refresh)
	}
	if len(progress) != 0 {
		t.Fatalf("progress = %q; upstream browser login notifies no progress", progress)
	}
}

// Upstream rejects a pasted redirect URL whose state differs from the flow's.
func TestLoginOpenAICodexRejectsManualInputWithWrongState(t *testing.T) {
	holdCodexCallbackPort(t)
	stubCodexTokenEndpoint(t)
	_, err := LoginOpenAICodex(context.Background(), OAuthLoginCallbacks{
		OnAuth: func(OAuthAuthInfo) {},
		OnManualCodeInput: func() (string, error) {
			return codexRedirectURI + "?code=c&state=not-the-flow-state", nil
		},
	})
	if err == nil || err.Error() != "State mismatch" {
		t.Fatalf("err = %v, want State mismatch", err)
	}
}

// Upstream throws "Missing authorization code" when no code arrives.
func TestLoginOpenAICodexEmptyManualInputIsMissingCode(t *testing.T) {
	holdCodexCallbackPort(t)
	stubCodexTokenEndpoint(t)
	_, err := LoginOpenAICodex(context.Background(), OAuthLoginCallbacks{
		OnAuth:            func(OAuthAuthInfo) {},
		OnManualCodeInput: func() (string, error) { return "  ", nil },
	})
	if err == nil || err.Error() != "Missing authorization code" {
		t.Fatalf("err = %v, want Missing authorization code", err)
	}
}
