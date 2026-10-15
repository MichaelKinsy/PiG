package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// truncatingServer answers with the status its first path segment names and a complete token JSON, but declares a longer body and closes the connection,
// so reading the body fails. Node's fetch rejects text() and json() then ("terminated"), whatever bytes arrived.
func truncatingServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)[0])
		if err != nil {
			t.Errorf("path %q names no status", r.URL.Path)
			status = http.StatusTeapot
		}
		body := `{"access_token":"a","refresh_token":"r","expires_in":60}`
		w.Header().Set("Content-Length", strconv.Itoa(len(body)+100))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// kimi-coding.ts reads a body through readJson (null when json() rejects) and response.text() (caught as ""): a truncated body is never
// parsed, even when the bytes that arrived are complete JSON.
func TestKimiTruncatedBodyReadsAsNoBody(t *testing.T) {
	server := truncatingServer(t)
	for _, tc := range []struct {
		status int
		run    func(kimiOAuthProvider) string
		want   string
	}{
		{200, func(p kimiOAuthProvider) string {
			_, err := p.RefreshTokenContext(context.Background(), OAuthCredentials{Refresh: "old"})
			return errorText(err)
		}, "Kimi Code token refresh response missing fields: null"},
		{200, func(p kimiOAuthProvider) string {
			_, err := p.startDeviceAuthorization(context.Background())
			return errorText(err)
		}, "Invalid Kimi Code device authorization response: null"},
		{500, func(p kimiOAuthProvider) string {
			_, err := p.startDeviceAuthorization(context.Background())
			return errorText(err)
		}, "Kimi Code device authorization failed with status 500"},
		{500, func(p kimiOAuthProvider) string {
			result, err := p.pollToken(context.Background(), "dc")
			if err != nil {
				return errorText(err)
			}
			return result.Message
		}, "Kimi Code device token request failed with status 500"},
	} {
		provider := kimiOAuthProvider{client: server.Client(), oauthHost: server.URL + "/" + strconv.Itoa(tc.status), sleep: func(context.Context, time.Duration) error { return nil }}
		if got := tc.run(provider); got != tc.want {
			t.Errorf("status %d: %q, want %q", tc.status, got, tc.want)
		}
	}
}

// xai.ts postForm reports a body whose json() rejects, a truncated one included, as invalid JSON.
func TestXaiTruncatedBodyIsInvalidJSON(t *testing.T) {
	server := truncatingServer(t)
	_, _, err := xaiOAuthProvider{client: server.Client()}.postForm(context.Background(), server.URL+"/200", formFields{{"a", "b"}})
	if got, want := errorText(err), "xAI OAuth returned invalid JSON (HTTP 200)"; got != want {
		t.Fatalf("postForm error = %q, want %q", got, want)
	}
}

func errorText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
