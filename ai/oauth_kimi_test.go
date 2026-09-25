package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestKimiOAuthLoginDeviceFlow(t *testing.T) {
	var tokenCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/api/oauth/device_authorization":
			if r.Method != http.MethodPost || r.Form.Get("client_id") != kimiOAuthClientID {
				t.Errorf("device request = %s %v", r.Method, r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"device_code":"device","user_code":"ABCD","verification_uri":"https://auth.example/activate","verification_uri_complete":"https://auth.example/activate?code=ABCD","interval":0.001,"expires_in":60}`))
		case "/api/oauth/token":
			tokenCalls.Add(1)
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.Form.Get("device_code") != "device" {
				t.Errorf("token form = %v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := kimiOAuthProvider{client: server.Client(), oauthHost: server.URL, sleep: func(context.Context, time.Duration) error { return nil }}
	var device OAuthDeviceCodeInfo
	creds, err := provider.Login(OAuthLoginCallbacks{OnDeviceCode: func(info OAuthDeviceCodeInfo) { device = info }})
	if err != nil {
		t.Fatal(err)
	}
	if device.UserCode != "ABCD" || device.VerificationURI != "https://auth.example/activate?code=ABCD" {
		t.Fatalf("device callback = %+v", device)
	}
	if creds.Access != "access" || creds.Refresh != "refresh" || creds.Expires <= time.Now().UnixMilli() {
		t.Fatalf("credentials = %+v", creds)
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("token calls = %d, want 1", tokenCalls.Load())
	}
}

func TestKimiOAuthRejectsUntrustedVerificationURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"device","user_code":"ABCD","verification_uri":"file:///tmp/token","verification_uri_complete":"file:///tmp/token","expires_in":60}`))
	}))
	defer server.Close()
	provider := kimiOAuthProvider{client: server.Client(), oauthHost: server.URL, sleep: func(context.Context, time.Duration) error { return nil }}
	if _, err := provider.Login(OAuthLoginCallbacks{}); err == nil || !strings.Contains(err.Error(), "invalid Kimi Code device authorization response") {
		t.Fatalf("Login() error = %v", err)
	}
}

func TestKimiOAuthRefreshRetriesTransientFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("refresh form = %v", r.Form)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"server_error"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":1800}`))
	}))
	defer server.Close()
	var sleeps []time.Duration
	provider := kimiOAuthProvider{client: server.Client(), oauthHost: server.URL, sleep: func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}}
	creds, err := provider.RefreshToken(OAuthCredentials{Refresh: "old-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if creds.Access != "new-access" || creds.Refresh != "new-refresh" {
		t.Fatalf("credentials = %+v", creds)
	}
	if !slices.Equal(sleeps, []time.Duration{time.Second}) {
		t.Fatalf("retry sleeps = %v", sleeps)
	}
}

func TestKimiOAuthRefreshUnauthorizedDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"expired"}`))
	}))
	defer server.Close()
	provider := kimiOAuthProvider{client: server.Client(), oauthHost: server.URL, sleep: func(context.Context, time.Duration) error { return nil }}
	_, err := provider.RefreshToken(OAuthCredentials{Refresh: "old-refresh"})
	if err == nil || !strings.Contains(err.Error(), "unauthorized (status 401): expired") {
		t.Fatalf("RefreshToken() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}
}

func TestTrustedHTTPURL(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{"https://example.test/path", true},
		{"http://127.0.0.1/callback", true},
		{"file:///tmp/token", false},
		{"javascript:alert(1)", false},
		{"not a URL", false},
	} {
		t.Run(url.PathEscape(test.value), func(t *testing.T) {
			if got := trustedHTTPURL(test.value); got != test.want {
				t.Fatalf("trustedHTTPURL(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func TestKimiOAuthLoginContextCancellation(t *testing.T) {
	for _, step := range []string{"before", "authorization", "first-wait", "poll"} {
		t.Run(step, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var requests []string
				provider := newKimiOAuthProvider()
				provider.oauthHost = "https://auth.kimi.test"
				provider.client = &http.Client{Transport: metaRoundTripper(func(request *http.Request) (*http.Response, error) {
					requests = append(requests, request.URL.Path)
					switch {
					case request.URL.Path == "/api/oauth/device_authorization" && step == "authorization",
						request.URL.Path == "/api/oauth/token" && step == "poll":
						return abortInFlight(request, cancel)
					case request.URL.Path == "/api/oauth/device_authorization":
						return metaJSONResponse(200, map[string]any{
							"device_code": "device", "user_code": "ABCD",
							"verification_uri": "https://auth.example/activate", "verification_uri_complete": "https://auth.example/activate?code=ABCD",
							"interval": 5, "expires_in": 60,
						}), nil
					default:
						return metaJSONResponse(200, map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600}), nil
					}
				})}
				if step == "before" {
					cancel()
				}
				notifications := 0
				start := time.Now()
				credentials, err := provider.LoginContext(ctx, OAuthLoginCallbacks{OnDeviceCode: func(OAuthDeviceCodeInfo) {
					notifications++
					if step == "first-wait" {
						time.AfterFunc(time.Second, cancel)
					}
				}})
				if err == nil || credentials != (OAuthCredentials{}) {
					t.Fatalf("LoginContext = %#v, %v; want cancellation", credentials, err)
				}
				wantRequests, wantNotifications, wantElapsed := []string(nil), 0, time.Duration(0)
				switch step {
				case "authorization":
					wantRequests, wantElapsed = []string{"/api/oauth/device_authorization"}, time.Second
				case "first-wait":
					wantRequests, wantNotifications, wantElapsed = []string{"/api/oauth/device_authorization"}, 1, time.Second
				case "poll":
					wantRequests, wantNotifications, wantElapsed = []string{"/api/oauth/device_authorization", "/api/oauth/token"}, 1, 6*time.Second
				}
				if elapsed := time.Since(start); elapsed != wantElapsed {
					t.Fatalf("returned after %v, want %v", elapsed, wantElapsed)
				}
				requestCount := len(requests)
				time.Sleep(10 * time.Minute)
				if len(requests) != requestCount || !slices.Equal(requests, wantRequests) || notifications != wantNotifications {
					t.Fatalf("requests=%v notifications=%d, want %v and %d", requests, notifications, wantRequests, wantNotifications)
				}
			})
		})
	}
}

func TestKimiOAuthRefreshTokenContextCancellation(t *testing.T) {
	for _, step := range []string{"before", "request", "backoff"} {
		t.Run(step, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				requests := 0
				provider := newKimiOAuthProvider()
				provider.oauthHost = "https://auth.kimi.test"
				provider.client = &http.Client{Transport: metaRoundTripper(func(request *http.Request) (*http.Response, error) {
					requests++
					if step == "request" {
						return abortInFlight(request, cancel)
					}
					time.AfterFunc(500*time.Millisecond, cancel)
					return metaJSONResponse(http.StatusInternalServerError, map[string]any{"error": "server_error"}), nil
				})}
				if step == "before" {
					cancel()
				}
				start := time.Now()
				credentials, err := provider.RefreshTokenContext(ctx, OAuthCredentials{Refresh: "refresh"})
				wantRequests, wantElapsed := 0, time.Duration(0)
				switch step {
				case "request":
					wantRequests, wantElapsed = 1, time.Second
				case "backoff":
					wantRequests, wantElapsed = 1, 500*time.Millisecond
				}
				if err == nil || credentials != (OAuthCredentials{}) || requests != wantRequests || time.Since(start) != wantElapsed {
					t.Fatalf("step=%s: refresh = %#v, %v after %v with %d requests", step, credentials, err, time.Since(start), requests)
				}
			})
		})
	}
}
