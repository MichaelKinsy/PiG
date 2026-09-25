package ai

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// orRoundTripper intercepts only the OpenRouter token endpoint, letting
// loopback callback requests reach the real transport.
type orRoundTripper struct {
	handler func(*http.Request) (*http.Response, error)
}

func (rt orRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "openrouter.ai" {
		return rt.handler(r)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func withOpenRouterToken(t *testing.T, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	prev := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: orRoundTripper{handler: handler}}
	t.Cleanup(func() { http.DefaultClient = prev })
}

func cannedResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestParseOpenRouterAuthorizationInput(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"redirect_url", "http://127.0.0.1:8976/oauth/callback/x?code=abc123&state=y", "abc123"},
		{"query_fragment", "code=fromquery&extra=1", "fromquery"},
		{"bare_code", "just-a-code", "just-a-code"},
		{"empty", "   ", ""},
		{"url_without_code", "https://openrouter.ai/auth?state=nope", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseOpenRouterAuthorizationInput(c.in); got != c.want {
				t.Fatalf("parse(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestOpenRouterPKCEChallengeIsS256(t *testing.T) {
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(pkce.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if pkce.Challenge != want {
		t.Fatalf("PKCE challenge is not S256(verifier): got %q want %q", pkce.Challenge, want)
	}
}

func TestOpenRouterExchangeSuccess(t *testing.T) {
	var gotBody map[string]string
	withOpenRouterToken(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != openRouterTokenURL {
			t.Fatalf("unexpected exchange request %s %s", r.Method, r.URL)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		return cannedResp(200, `{"key":"sk-or-permanent"}`), nil
	})

	creds, err := exchangeOpenRouterCode(t.Context(), "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if creds.Access != "sk-or-permanent" || creds.Refresh != "" {
		t.Fatalf("creds = %+v", creds)
	}
	if creds.Expires != openRouterMaxSafeInteger {
		t.Fatalf("expires = %d, want MAX_SAFE_INTEGER %d", creds.Expires, openRouterMaxSafeInteger)
	}
	if gotBody["code"] != "the-code" || gotBody["code_verifier"] != "the-verifier" || gotBody["code_challenge_method"] != "S256" {
		t.Fatalf("PKCE exchange body = %+v", gotBody)
	}
}

func TestOpenRouterExchangeErrorBody(t *testing.T) {
	cases := []struct {
		name, body, wantSub string
		status              int
	}{
		{"error_object_message", `{"error":{"message":"bad grant"}}`, "bad grant", 400},
		{"error_description", `{"error_description":"expired code"}`, "expired code", 403},
		{"message_field", `{"message":"nope"}`, "nope", 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withOpenRouterToken(t, func(*http.Request) (*http.Response, error) {
				return cannedResp(c.status, c.body), nil
			})
			_, err := exchangeOpenRouterCode(t.Context(), "c", "v")
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), c.wantSub) || !strings.Contains(err.Error(), "key exchange failed") {
				t.Fatalf("error = %q, want detail %q", err.Error(), c.wantSub)
			}
		})
	}
}

func TestOpenRouterExchangeMissingKey(t *testing.T) {
	withOpenRouterToken(t, func(*http.Request) (*http.Response, error) {
		return cannedResp(200, `{"not_a_key":"x"}`), nil
	})
	_, err := exchangeOpenRouterCode(t.Context(), "c", "v")
	if err == nil || !strings.Contains(err.Error(), `carries no "key"`) {
		t.Fatalf("expected no-key error, got %v", err)
	}
}

func TestOpenRouterExchangeInvalidJSON(t *testing.T) {
	withOpenRouterToken(t, func(*http.Request) (*http.Response, error) {
		return cannedResp(200, `not json at all`), nil
	})
	_, err := exchangeOpenRouterCode(t.Context(), "c", "v")
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected invalid-JSON error, got %v", err)
	}
}

// TestOpenRouterCancelWaitRespectsClaim is the claimed-vs-manual guard: a
// claimed callback must not be handed to manual paste, and an unclaimed one
// must be.
func TestOpenRouterCancelWaitRespectsClaim(t *testing.T) {
	claimed := &openRouterCallback{resultCh: make(chan openRouterResult, 1), done: make(chan struct{})}
	claimed.claimed = true
	claimed.cancelWait()
	select {
	case <-claimed.resultCh:
		t.Fatal("cancelWait handed a claimed callback to manual")
	default:
	}

	unclaimed := &openRouterCallback{resultCh: make(chan openRouterResult, 1), done: make(chan struct{})}
	unclaimed.cancelWait()
	select {
	case r := <-unclaimed.resultCh:
		if r.cred != nil || r.err != nil {
			t.Fatalf("manual hand-off must be a nil result, got %+v", r)
		}
	default:
		t.Fatal("cancelWait did not hand an unclaimed callback to manual")
	}
}

func TestOpenRouterLoginManualFallback(t *testing.T) {
	withOpenRouterToken(t, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		return cannedResp(200, `{"key":"key-for-`+body["code"]+`"}`), nil
	})

	creds, err := LoginOpenRouter(t.Context(), OAuthLoginCallbacks{
		OnAuth: func(OAuthAuthInfo) {}, // no browser callback arrives
		OnManualCodeInput: func() (string, error) {
			return "https://x/cb?code=MANUAL", nil
		},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if creds.Access != "key-for-MANUAL" {
		t.Fatalf("manual fallback did not win: %+v", creds)
	}
}

// TestOpenRouterClaimedCallbackWinsOverManual drives a real loopback callback
// that claims and completes the exchange before manual paste resolves; the
// browser credential must win.
func TestOpenRouterClaimedCallbackWinsOverManual(t *testing.T) {
	withOpenRouterToken(t, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		return cannedResp(200, `{"key":"key-for-`+body["code"]+`"}`), nil
	})

	creds, err := LoginOpenRouter(t.Context(), OAuthLoginCallbacks{
		OnAuth: func(info OAuthAuthInfo) {
			// Synchronously complete the browser callback before manual runs.
			u, perr := url.Parse(info.URL)
			if perr != nil {
				t.Errorf("authorize url: %v", perr)
				return
			}
			cbURL := u.Query().Get("callback_url")
			resp, gerr := http.Get(cbURL + "?code=BROWSER")
			if gerr != nil {
				t.Errorf("browser callback: %v", gerr)
				return
			}
			_ = resp.Body.Close()
		},
		OnManualCodeInput: func() (string, error) {
			return "code=MANUAL", nil
		},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if creds.Access != "key-for-BROWSER" {
		t.Fatalf("claimed callback did not win over manual: %+v", creds)
	}
}

func TestOpenRouterRefreshKeepsPermanentKey(t *testing.T) {
	// An OpenRouter API key does not expire and has no refresh token.
	creds := OAuthCredentials{Access: "sk", Expires: openRouterMaxSafeInteger}
	refreshed, err := OpenRouterOAuthProvider{}.RefreshToken(creds)
	if err != nil || refreshed != creds {
		t.Fatalf("refresh changed a permanent key: %+v %v", refreshed, err)
	}
}

// TestOpenRouterRegisteredForLogin proves openrouter is in the OAuth registry so
// it appears in /login, matching upstream (providers/openrouter.ts declares
// auth.oauth). Before this it was API-key-only.
func TestOpenRouterRegisteredForLogin(t *testing.T) {
	p, ok := GetOAuthProvider("openrouter")
	if !ok {
		t.Fatal("openrouter OAuth provider not registered")
	}
	if p.ID() != "openrouter" || !p.UsesCallbackServer() {
		t.Fatalf("unexpected provider %+v", p)
	}
	found := false
	for _, q := range GetOAuthProviders() {
		if q.ID() == "openrouter" {
			found = true
		}
	}
	if !found {
		t.Fatal("openrouter missing from GetOAuthProviders (the /login source)")
	}
}
