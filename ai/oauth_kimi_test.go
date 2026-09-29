package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Ports packages/ai/test/kimi-coding-oauth.test.ts:52,121,143,165,194,231.
func TestKimiOAuthUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, tokenError, host string
		interval               int
	}{
		{"logs in with the device authorization flow", "", "https://auth.kimi.com", 5},
		{"fails when the device code expires", "expired_token", "https://auth.kimi.com", 5},
		{"fails when the user denies the login", "access_denied", "https://auth.kimi.com", 5},
		{"honors the KIMI_CODE_OAUTH_HOST override", "", "https://auth.example.com", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KIMI_CODE_OAUTH_HOST", tc.host+"/")
			synctest.Test(t, func(t *testing.T) {
				p := newKimiOAuthProvider()
				if tc.tokenError == "" && tc.interval == 5 {
					startTime := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
					time.Sleep(time.Until(startTime))
				}
				start := time.Now()
				var urls []string
				var polls []time.Duration
				var pollMu sync.Mutex
				pollTimes := func() []time.Duration {
					pollMu.Lock()
					defer pollMu.Unlock()
					return slices.Clone(polls)
				}
				p.client = &http.Client{Transport: metaRoundTripper(func(r *http.Request) (*http.Response, error) {
					urls = append(urls, r.URL.String())
					if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Accept") != "application/json" {
						t.Errorf("request = %s %v", r.Method, r.Header)
					}
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.Form.Get("client_id") != "17e5f671-d194-4dfb-9706-5516cb48c098" {
						t.Errorf("client_id = %s", r.Form.Get("client_id"))
					}
					switch r.URL.String() {
					case tc.host + "/api/oauth/device_authorization":
						return metaJSONResponse(200, map[string]any{"device_code": "device-code-123", "user_code": "ABCD-1234", "verification_uri": "https://www.kimi.com/code", "verification_uri_complete": "https://www.kimi.com/code?user_code=ABCD-1234", "interval": tc.interval, "expires_in": 600}), nil
					case tc.host + "/api/oauth/token":
						pollMu.Lock()
						polls = append(polls, time.Since(start))
						pollCount := len(polls)
						pollMu.Unlock()
						if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.Form.Get("device_code") != "device-code-123" {
							t.Errorf("token form = %v", r.Form)
						}
						if tc.tokenError != "" {
							return metaJSONResponse(400, map[string]any{"error": tc.tokenError}), nil
						}
						if tc.interval == 5 && pollCount == 1 {
							return metaJSONResponse(400, map[string]any{"error": "authorization_pending"}), nil
						}
						if tc.interval == 1 {
							return metaJSONResponse(200, map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 60}), nil
						}
						return metaJSONResponse(200, map[string]any{"access_token": "access-token", "refresh_token": "refresh-token", "expires_in": 3600}), nil
					default:
						t.Fatalf("unexpected URL %s", r.URL)
						return nil, nil
					}
				})}
				var notifications []AuthEvent
				var creds Credential
				var loginErr error
				go func() {
					creds, loginErr = oauthNativeLogin(p)(t.Context(), AuthInteraction{Notify: func(event AuthEvent) { notifications = append(notifications, event) }, Prompt: func(context.Context, AuthPrompt) (string, error) {
						t.Error("Kimi login must not prompt")
						return "", nil
					}})
				}()
				synctest.Wait()
				wantInfo := AuthDeviceCodeEvent{UserCode: "ABCD-1234", VerificationURI: "https://www.kimi.com/code?user_code=ABCD-1234", IntervalSeconds: new(float64(tc.interval)), ExpiresInSeconds: new(float64(600))}
				if !reflect.DeepEqual(notifications, []AuthEvent{wantInfo}) {
					t.Fatalf("notifications = %#v", notifications)
				}
				time.Sleep(time.Duration(tc.interval)*time.Second - time.Millisecond)
				synctest.Wait()
				if got := pollTimes(); len(got) != 0 {
					t.Fatalf("early polls = %v", got)
				}
				time.Sleep(time.Millisecond)
				synctest.Wait()
				if tc.tokenError != "" {
					want := "expired"
					if tc.tokenError == "access_denied" {
						want = "denied"
					}
					if loginErr == nil || !strings.Contains(loginErr.Error(), want) {
						t.Fatalf("login error = %v", loginErr)
					}
					return
				}
				wantPolls := []time.Duration{time.Second}
				wantCreds := Credential{Type: CredentialOAuth, Access: "a", Refresh: "r", Expires: start.Add(61 * time.Second).UnixMilli()}
				if tc.interval == 5 {
					if got := pollTimes(); !slices.Equal(got, []time.Duration{5 * time.Second}) {
						t.Fatalf("first polls = %v", got)
					}
					time.Sleep(5 * time.Second)
					synctest.Wait()
					wantPolls = []time.Duration{5 * time.Second, 10 * time.Second}
					wantCreds = Credential{Type: CredentialOAuth, Access: "access-token", Refresh: "refresh-token", Expires: start.Add(3610 * time.Second).UnixMilli()}
				}
				if got := pollTimes(); loginErr != nil || !reflect.DeepEqual(creds, wantCreds) || !slices.Equal(got, wantPolls) {
					t.Fatalf("creds=%#v err=%v polls=%v", creds, loginErr, got)
				}
				if tc.interval == 1 && !slices.Equal(urls, []string{tc.host + "/api/oauth/device_authorization", tc.host + "/api/oauth/token"}) {
					t.Fatalf("urls = %v", urls)
				}
			})
		})
	}
	t.Run("refreshes tokens and returns a Bearer header for requests", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := newKimiOAuthProvider()
			p.oauthHost = "https://auth.kimi.com"
			p.client = &http.Client{Transport: metaRoundTripper(func(r *http.Request) (*http.Response, error) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.URL.String() != "https://auth.kimi.com/api/oauth/token" || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" || r.Form.Get("client_id") != kimiOAuthClientID {
					t.Errorf("request = %s %v", r.URL, r.Form)
				}
				return metaJSONResponse(200, map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600}), nil
			})}
			before := time.Now()
			creds, err := oauthRefresh(p)(t.Context(), Credential{Type: CredentialOAuth, Access: "old-access", Refresh: "old-refresh", Expires: before.UnixMilli()})
			if err != nil || creds.Type != CredentialOAuth || creds.Access != "new-access" || creds.Refresh != "new-refresh" || creds.Expires < before.Add(time.Hour).UnixMilli() {
				t.Fatalf("refresh = %#v %v", creds, err)
			}
			auth, err := oauthToAuth("kimi-coding", p)(creds)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(auth, ModelAuth{Headers: ProviderHeaders{"Authorization": new("Bearer new-access")}}) {
				t.Fatalf("auth = %#v", auth)
			}
		})
	})
	t.Run("retries refresh on 429 and fails unauthorized on invalid_grant", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := newKimiOAuthProvider()
			calls := 0
			invalid := false
			p.client = &http.Client{Transport: metaRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				if invalid {
					return metaJSONResponse(400, map[string]any{"error": "invalid_grant"}), nil
				}
				if calls == 1 {
					return metaJSONResponse(429, map[string]any{"error": "temporarily_unavailable"}), nil
				}
				return metaJSONResponse(200, map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 60}), nil
			})}
			start := time.Now()
			creds, err := p.RefreshTokenContext(t.Context(), OAuthCredentials{Access: "old", Refresh: "old"})
			if err != nil || creds.Access != "a" || calls != 2 || time.Since(start) != time.Second {
				t.Fatalf("refresh = %#v %v calls=%d elapsed=%s", creds, err, calls, time.Since(start))
			}
			invalid = true
			calls = 0
			_, err = p.RefreshTokenContext(t.Context(), OAuthCredentials{Access: "old", Refresh: "old"})
			if err == nil || !strings.Contains(err.Error(), "unauthorized") || calls != 1 {
				t.Fatalf("invalid_grant = %v calls=%d", err, calls)
			}
		})
	})
}

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
				if err == nil || !reflect.DeepEqual(credentials, OAuthCredentials{}) {
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
				if err == nil || !reflect.DeepEqual(credentials, OAuthCredentials{}) || requests != wantRequests || time.Since(start) != wantElapsed {
					t.Fatalf("step=%s: refresh = %#v, %v after %v with %d requests", step, credentials, err, time.Since(start), requests)
				}
			})
		})
	}
}
