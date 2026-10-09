package ai

// pi: packages/ai/src/auth/oauth/openai-codex.ts

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	stdjson "encoding/json"
)

// Builtin flows compute `expires` with JavaScript arithmetic on the parsed response: Date.now() + expires_in * 1000 - skew (anthropic.ts:230,351, openai-codex.ts:145, kimi-coding.ts:138, radius.ts:137, xai.ts:141, github-copilot.ts:345). A fraction survives, a numeric string coerces, and a missing value is NaN, which JSON.stringify writes as null.
func TestBuiltinOAuthExpiryUsesJavaScriptArithmetic(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		for _, tc := range []struct {
			body  string
			delta float64
		}{
			{`"expires_in":0.0015`, 1.5 - 300_000},
			{`"expires_in":"3600"`, 3_600_000 - 300_000},
		} {
			synctest.Test(t, func(t *testing.T) {
				withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
					return codexJSONResp(200, `{"access_token":"a","refresh_token":"r",`+tc.body+`}`), nil
				})
				creds, err := RefreshAnthropicToken(t.Context(), "r")
				if err != nil {
					t.Fatalf("%s: %v", tc.body, err)
				}
				if got, want := creds.ExpiresMillis(), float64(time.Now().UnixMilli())+tc.delta; got != want {
					t.Fatalf("%s: expires = %v, want %v", tc.body, got, want)
				}
			})
		}
	})
	t.Run("openai-codex", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
				return codexJSONResp(200, `{"access_token":"`+codexTestToken(t, "account")+`","refresh_token":"r","expires_in":0.0015}`), nil
			})
			creds, err := (CodexOAuthProvider{}).RefreshTokenContext(t.Context(), OAuthCredentials{Refresh: "r"})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := creds.ExpiresMillis(), float64(time.Now().UnixMilli())+1.5; got != want {
				t.Fatalf("expires = %v, want %v", got, want)
			}
		})
	})
	t.Run("radius", func(t *testing.T) {
		for _, tc := range []struct {
			body string
			want func(now float64) float64
		}{
			{`"expires_in":0.0005,`, func(now float64) float64 { return now + 0.5 - 60_000 }},
			{``, func(float64) float64 { return math.NaN() }},
		} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r",` + tc.body + `"scope":"s"}`))
			}))
			oauth := CreateRadiusOAuth(RadiusOAuthOptions{Gateway: server.URL})
			now := time.UnixMilli(1_700_000_000_000)
			oauth.now = func() time.Time { return now }
			creds, err := oauth.RefreshTokenContext(t.Context(), OAuthCredentials{Refresh: "r"})
			server.Close()
			if err != nil {
				t.Fatalf("%q: %v", tc.body, err)
			}
			got, want := creds.ExpiresMillis(), tc.want(float64(now.UnixMilli()))
			if got != want && !(math.IsNaN(got) && math.IsNaN(want)) {
				t.Fatalf("%q: expires = %v, want %v", tc.body, got, want)
			}
			if math.IsNaN(want) {
				encoded, _ := stdjson.Marshal(creds)
				var object map[string]stdjson.RawMessage
				_ = stdjson.Unmarshal(encoded, &object)
				if string(object["expires"]) != "null" {
					t.Fatalf("NaN expires serialized as %s, want null", encoded)
				}
			}
		}
	})
}

// github-copilot.ts:330-346 rejects a falsy or non-object body with "Invalid Copilot token response", rejects a non-string token or non-number expires_at (including an array body) with "Invalid Copilot token response fields", and otherwise stores expires_at * 1000 - 5 * 60 * 1000 as a JavaScript number: a fraction survives, an empty token is a string, and an overflowing literal parses to Infinity, which JSON.stringify writes as null.
func TestCopilotTokenResponseMatchesPi(t *testing.T) {
	for _, tc := range []struct {
		body    string
		err     string
		expires string
	}{
		{body: `null`, err: "Invalid Copilot token response"},
		{body: `0`, err: "Invalid Copilot token response"},
		{body: `""`, err: "Invalid Copilot token response"},
		{body: `"token"`, err: "Invalid Copilot token response"},
		{body: `true`, err: "Invalid Copilot token response"},
		{body: `[]`, err: "Invalid Copilot token response fields"},
		{body: `{"token":1,"expires_at":1}`, err: "Invalid Copilot token response fields"},
		{body: `{"token":"t","expires_at":"1"}`, err: "Invalid Copilot token response fields"},
		{body: `{"token":"t","expires_at":1700000000.0005}`, expires: "1699999700000.5"},
		{body: `{"token":"","expires_at":1700000000}`, expires: "1699999700000"},
		{body: `{"token":"t","expires_at":1e400}`, expires: "null"},
	} {
		withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
			return codexJSONResp(200, tc.body), nil
		})
		credential, err := refreshCopilotAccessToken(t.Context(), "github-token", "")
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("%s: error %v, want %q", tc.body, err, tc.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.body, err)
			continue
		}
		encoded, _ := stdjson.Marshal(credential)
		var object map[string]stdjson.RawMessage
		_ = stdjson.Unmarshal(encoded, &object)
		if got := string(object["expires"]); got != tc.expires {
			t.Errorf("%s: expires %s, want %s", tc.body, got, tc.expires)
		}
	}
}

// openai-codex.ts:127-146 rejects a token response unless access_token and refresh_token are truthy and expires_in has typeof "number", and reports JSON.stringify(json) of the parsed body: a string expires_in, null, an array and a scalar are "missing fields", the message is the compact re-serialization, and an overflowing literal parses to Infinity, which passes the type check and is stored as null.
func TestCodexTokenResponseMatchesPi(t *testing.T) {
	for _, tc := range []struct {
		body    string
		err     string
		expires string
	}{
		{body: `{"access_token":"a","refresh_token":"r","expires_in":"3600"}`, err: `{"access_token":"a","refresh_token":"r","expires_in":"3600"}`},
		{body: "{ \"access_token\" : \"a\",\n \"refresh_token\": \"\", \"expires_in\": 1.0 }", err: `{"access_token":"a","refresh_token":"","expires_in":1}`},
		{body: `null`, err: `null`},
		{body: `[]`, err: `[]`},
		{body: `0`, err: `0`},
		{body: `"tok"`, err: `"tok"`},
		{body: `true`, err: `true`},
		{body: `{"access_token":"a","refresh_token":"r"}`, err: `{"access_token":"a","refresh_token":"r"}`},
		{body: `{"access_token":"a","refresh_token":"r","expires_in":null}`, err: `{"access_token":"a","refresh_token":"r","expires_in":null}`},
		{body: `{"access_token":"a","refresh_token":"r","expires_in":1e999}`, expires: "null"},
	} {
		withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
			return codexJSONResp(200, tc.body), nil
		})
		for _, operation := range []string{"refresh", "exchange"} {
			var creds OAuthCredentials
			var err error
			if operation == "refresh" {
				creds, err = RefreshCodexToken(t.Context(), "r")
			} else {
				creds, err = exchangeCodexAuthorizationCode(t.Context(), "code", "verifier", "http://localhost/cb")
			}
			if tc.err != "" {
				if want := "OpenAI Codex token " + operation + " response missing fields: " + tc.err; err == nil || err.Error() != want {
					t.Errorf("%s %q: error %v, want %q", operation, tc.body, err, want)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s %q: %v", operation, tc.body, err)
				continue
			}
			encoded, _ := stdjson.Marshal(creds)
			var object map[string]stdjson.RawMessage
			_ = stdjson.Unmarshal(encoded, &object)
			if got := string(object["expires"]); got != tc.expires {
				t.Errorf("%s %q: expires %s, want %s", operation, tc.body, got, tc.expires)
			}
		}
	}
}
