package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// bodyRecorder serves a fixed JSON answer and keeps the raw request bodies.
type bodyRecorder struct {
	mu     sync.Mutex
	bodies []string
}

func (recorder *bodyRecorder) server(t *testing.T, answer string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		recorder.mu.Lock()
		recorder.bodies = append(recorder.bodies, string(body))
		recorder.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	return server
}

// Pi builds every OAuth form body with `new URLSearchParams({...})` (auth/oauth/xai.ts:73,146-151,168-173,214-219,
// kimi-coding.ts:45-47,159-163,232-236, meta.ts:80,115-119, radius.ts:162-168,195,241-245,307-311): fields keep their
// insertion order and the WHATWG serializer keeps `*` and encodes `~`. url.Values.Encode sorted the keys and used
// QueryEscape. The refresh token below has a `*`, `~` and a space to show the encoder as well.
func TestOAuthFormBodiesKeepPiFieldOrderAndEncoding(t *testing.T) {
	const refresh = "r*~ x"
	const encoded = "r*%7E+x"
	t.Run("xai refresh", func(t *testing.T) {
		var recorder bodyRecorder
		server := recorder.server(t, `{"access_token":"a","refresh_token":"b","expires_in":3600}`)
		provider := testXaiProvider(server.URL, server.URL)
		if _, err := provider.RefreshToken(OAuthCredentials{Refresh: refresh}); err != nil {
			t.Fatal(err)
		}
		want := "grant_type=refresh_token&client_id=" + xaiClientID + "&refresh_token=" + encoded
		if recorder.bodies[0] != want {
			t.Fatalf("body = %s, want %s", recorder.bodies[0], want)
		}
	})
	t.Run("xai poll", func(t *testing.T) {
		var recorder bodyRecorder
		server := recorder.server(t, `{"device_code":"d*","user_code":"U","verification_uri":"https://auth.x.ai/device","expires_in":60,"interval":1,"access_token":"a","refresh_token":"b"}`)
		provider := testXaiProvider(server.URL, server.URL)
		if _, err := provider.pollToken(t.Context(), "d*~"); err != nil {
			t.Fatal(err)
		}
		if want := "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code&client_id=" + xaiClientID + "&device_code=d*%7E"; recorder.bodies[0] != want {
			t.Fatalf("poll body = %s, want %s", recorder.bodies[0], want)
		}
	})
	t.Run("kimi refresh and poll", func(t *testing.T) {
		var recorder bodyRecorder
		server := recorder.server(t, `{"access_token":"a","refresh_token":"b","expires_in":3600}`)
		provider := kimiOAuthProvider{client: server.Client(), oauthHost: server.URL, sleep: func(context.Context, time.Duration) error { return nil }}
		if _, err := provider.RefreshToken(OAuthCredentials{Refresh: refresh}); err != nil {
			t.Fatal(err)
		}
		if _, err := provider.pollToken(t.Context(), "d*~"); err != nil {
			t.Fatal(err)
		}
		wantRefresh := "client_id=" + kimiOAuthClientID + "&grant_type=refresh_token&refresh_token=" + encoded
		wantPoll := "client_id=" + kimiOAuthClientID + "&device_code=d*%7E&grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code"
		if recorder.bodies[0] != wantRefresh || recorder.bodies[1] != wantPoll {
			t.Fatalf("bodies = %q, want %q and %q", recorder.bodies, wantRefresh, wantPoll)
		}
	})
	t.Run("meta device token", func(t *testing.T) {
		var recorder bodyRecorder
		server := recorder.server(t, `{"access_token":"tok"}`)
		provider := metaOAuthProvider{client: server.Client(), deviceTokenURL: server.URL}
		if _, err := provider.pollIdentityToken(t.Context(), "d*~"); err != nil {
			t.Fatal(err)
		}
		if want := "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code&device_code=d*%7E&client_id=" + metaClientID; recorder.bodies[0] != want {
			t.Fatalf("body = %s, want %s", recorder.bodies[0], want)
		}
	})
	t.Run("radius refresh", func(t *testing.T) {
		var recorder bodyRecorder
		server := recorder.server(t, `{"access_token":"a","refresh_token":"b","expires_in":3600}`)
		if _, err := newTestRadiusOAuth(server.URL, time.Now()).RefreshToken(OAuthCredentials{Refresh: refresh}); err != nil {
			t.Fatal(err)
		}
		if want := "grant_type=refresh_token&client_id=" + radiusOAuthClientID + "&refresh_token=" + encoded; recorder.bodies[0] != want {
			t.Fatalf("body = %s, want %s", recorder.bodies[0], want)
		}
	})
}
