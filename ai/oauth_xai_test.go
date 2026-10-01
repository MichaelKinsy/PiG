package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

func testXaiProvider(deviceURL, tokenURL string) xaiOAuthProvider {
	return xaiOAuthProvider{
		client:    http.DefaultClient,
		deviceURL: deviceURL,
		tokenURL:  tokenURL,
		now:       func() time.Time { return time.Unix(1_000, 0) },
	}
}

func upstreamXaiDeviceCode() map[string]any {
	return map[string]any{"device_code": "device-code", "user_code": "ABCD-1234", "verification_uri": "https://accounts.x.ai/oauth2/device", "expires_in": 900, "interval": 5}
}

func upstreamXaiToken() map[string]any {
	return map[string]any{"access_token": "access-token", "refresh_token": "refresh-token", "expires_in": 21600, "token_type": "Bearer"}
}

func upstreamXaiProvider(t *testing.T, transport metaRoundTripper) xaiOAuthProvider {
	t.Helper()
	start := time.Now()
	epoch := time.Date(2026, time.July, 9, 20, 0, 0, 0, time.UTC)
	p := newXaiOAuthProvider()
	p.client = &http.Client{Transport: transport}
	p.now = func() time.Time { return epoch.Add(time.Since(start)) }
	return p
}

func assertXaiForm(t *testing.T, r *http.Request, endpoint string, want url.Values) {
	t.Helper()
	if r.URL.String() != endpoint {
		t.Fatalf("request URL = %s, want %s", r.URL, endpoint)
	}
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.PostForm, want) {
		t.Fatalf("form = %#v, want %#v", r.PostForm, want)
	}
}

// upstream: packages/ai/test/xai-oauth.test.ts:85. The server's slow_down interval owns the subsequent poll schedule.
func TestXaiOAuthDeviceGrantTimingUpstream(t *testing.T) {
	for _, interval := range []int{10, 12} {
		t.Run(fmt.Sprintf("server interval %d", interval), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var polls []time.Time
				var pollsMu sync.Mutex
				pollSnapshot := func() []time.Time {
					pollsMu.Lock()
					defer pollsMu.Unlock()
					return slices.Clone(polls)
				}
				var p xaiOAuthProvider
				p = upstreamXaiProvider(t, func(r *http.Request) (*http.Response, error) {
					if r.URL.String() == "https://auth.x.ai/oauth2/device/code" {
						assertXaiForm(t, r, "https://auth.x.ai/oauth2/device/code", url.Values{
							"client_id": {"b1a00492-073a-47ea-816f-4c329264a828"}, "scope": {"openid profile email offline_access grok-cli:access api:access"}, "referrer": {pigidentity.XAIReferrer},
						})
						return metaJSONResponse(200, upstreamXaiDeviceCode()), nil
					}
					assertXaiForm(t, r, "https://auth.x.ai/oauth2/token", url.Values{
						"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {"b1a00492-073a-47ea-816f-4c329264a828"}, "device_code": {"device-code"},
					})
					pollsMu.Lock()
					polls = append(polls, p.now())
					pollCount := len(polls)
					pollsMu.Unlock()
					switch pollCount {
					case 1:
						return metaJSONResponse(400, map[string]any{"error": "authorization_pending"}), nil
					case 2:
						return metaJSONResponse(400, map[string]any{"error": "slow_down", "interval": interval}), nil
					case 3:
						return metaJSONResponse(200, upstreamXaiToken()), nil
					default:
						t.Fatal("unexpected token poll")
						return nil, nil
					}
				})
				var devices []OAuthDeviceCodeInfo
				var credential OAuthCredentials
				var loginErr error
				start := p.now()
				done := make(chan struct{})
				go func() {
					defer close(done)
					credential, loginErr = p.LoginContext(t.Context(), OAuthLoginCallbacks{OnDeviceCode: func(info OAuthDeviceCodeInfo) { devices = append(devices, info) }})
				}()
				synctest.Wait()
				wantDevices := []OAuthDeviceCodeInfo{{UserCode: "ABCD-1234", VerificationURI: "https://accounts.x.ai/oauth2/device", IntervalSeconds: 5, ExpiresInSeconds: 900}}
				if !reflect.DeepEqual(devices, wantDevices) || len(pollSnapshot()) != 0 {
					t.Fatalf("devices=%#v polls=%v", devices, pollSnapshot())
				}
				var wantPolls []time.Time
				for _, delay := range []time.Duration{5 * time.Second, 5 * time.Second, time.Duration(interval) * time.Second} {
					time.Sleep(delay)
					synctest.Wait()
					wantPolls = append(wantPolls, p.now())
					if got := pollSnapshot(); !slices.Equal(got, wantPolls) {
						t.Fatalf("poll times = %v, want %v", got, wantPolls)
					}
				}
				<-done
				want := OAuthCredentials{Access: "access-token", Refresh: "refresh-token", Expires: start.UnixMilli() + int64(10+interval)*1000 + 21600000 - 300000}
				if loginErr != nil || !reflect.DeepEqual(credential, want) {
					t.Fatalf("login = %#v, %v; want %#v", credential, loginErr, want)
				}
			})
		})
	}
}

func TestXaiOAuthUpstream(t *testing.T) {
	// upstream: packages/ai/test/xai-oauth.test.ts:158.
	t.Run("falls back to the default poll interval when the response reports interval 0", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var polls []time.Time
			var p xaiOAuthProvider
			p = upstreamXaiProvider(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == "https://auth.x.ai/oauth2/device/code" {
					body := upstreamXaiDeviceCode()
					body["interval"] = 0
					return metaJSONResponse(200, body), nil
				}
				polls = append(polls, p.now())
				return metaJSONResponse(200, upstreamXaiToken()), nil
			})
			start := p.now()
			if _, err := p.LoginContext(t.Context(), OAuthLoginCallbacks{}); err != nil {
				t.Fatal(err)
			}
			if want := []time.Time{start.Add(5 * time.Second)}; !slices.Equal(polls, want) {
				t.Fatalf("poll times = %v, want %v", polls, want)
			}
		})
	})
	// upstream: packages/ai/test/xai-oauth.test.ts:181.
	t.Run("prefers verification_uri_complete when the server provides it", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := upstreamXaiProvider(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == "https://auth.x.ai/oauth2/device/code" {
					body := upstreamXaiDeviceCode()
					body["verification_uri_complete"] = "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234"
					return metaJSONResponse(200, body), nil
				}
				return metaJSONResponse(200, upstreamXaiToken()), nil
			})
			var devices []OAuthDeviceCodeInfo
			if _, err := p.LoginContext(t.Context(), OAuthLoginCallbacks{OnDeviceCode: func(info OAuthDeviceCodeInfo) { devices = append(devices, info) }}); err != nil {
				t.Fatal(err)
			}
			want := []OAuthDeviceCodeInfo{{UserCode: "ABCD-1234", VerificationURI: "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234", IntervalSeconds: 5, ExpiresInSeconds: 900}}
			if !reflect.DeepEqual(devices, want) {
				t.Fatalf("device notifications = %#v, want %#v", devices, want)
			}
		})
	})
	// upstream: packages/ai/test/xai-oauth.test.ts:211,226.
	for _, tc := range []struct{ field, uri string }{
		{"verification_uri_complete", "http://accounts.x.ai/oauth2/device?user_code=ABCD-1234"},
		{"verification_uri", "http://accounts.x.ai/oauth2/device"},
		{"verification_uri", "file:///etc/passwd"},
		{"verification_uri", "not a url"},
	} {
		t.Run("rejects a non-https "+tc.field+": "+tc.uri, func(t *testing.T) {
			p := upstreamXaiProvider(t, func(*http.Request) (*http.Response, error) {
				body := upstreamXaiDeviceCode()
				body[tc.field] = tc.uri
				return metaJSONResponse(200, body), nil
			})
			_, err := p.LoginContext(t.Context(), OAuthLoginCallbacks{})
			if err == nil || !strings.Contains(err.Error(), "Untrusted verification URI") {
				t.Fatalf("login error = %v", err)
			}
		})
	}
	// upstream: packages/ai/test/xai-oauth.test.ts:238.
	for _, code := range []string{"access_denied", "authorization_denied"} {
		t.Run("fails when device authorization is denied: "+code, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				requests := 0
				p := upstreamXaiProvider(t, func(*http.Request) (*http.Response, error) {
					requests++
					if requests == 1 {
						body := upstreamXaiDeviceCode()
						body["interval"] = 1
						return metaJSONResponse(200, body), nil
					}
					return metaJSONResponse(400, map[string]any{"error": code}), nil
				})
				start := p.now()
				_, err := p.LoginContext(t.Context(), OAuthLoginCallbacks{})
				if err == nil || err.Error() != "xAI device authorization was denied" || requests != 2 || p.now().Sub(start) != time.Second {
					t.Fatalf("login error = %v, requests = %d, elapsed = %v", err, requests, p.now().Sub(start))
				}
			})
		})
	}
	// upstream: packages/ai/test/xai-oauth.test.ts:260.
	t.Run("cancels while waiting for the first token poll", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			requests := 0
			p := upstreamXaiProvider(t, func(*http.Request) (*http.Response, error) {
				requests++
				return metaJSONResponse(200, upstreamXaiDeviceCode()), nil
			})
			_, err := p.LoginContext(ctx, OAuthLoginCallbacks{OnDeviceCode: func(OAuthDeviceCodeInfo) { cancel() }})
			if err == nil || err.Error() != "Login cancelled" || requests != 1 {
				t.Fatalf("login error = %v, requests = %d", err, requests)
			}
		})
	})
	// upstream: packages/ai/test/xai-oauth.test.ts:275.
	t.Run("refreshes tokens and preserves an unrotated refresh token", func(t *testing.T) {
		requests := 0
		p := upstreamXaiProvider(t, func(r *http.Request) (*http.Response, error) {
			requests++
			refresh := "old-refresh"
			body := upstreamXaiToken()
			body["access_token"], body["refresh_token"] = "new-access", "new-refresh"
			if requests == 2 {
				refresh = "keep-refresh"
				body["access_token"] = "newer-access"
				delete(body, "refresh_token")
			}
			assertXaiForm(t, r, "https://auth.x.ai/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {"b1a00492-073a-47ea-816f-4c329264a828"}, "refresh_token": {refresh}})
			return metaJSONResponse(200, body), nil
		})
		refresh := oauthRefresh(p)
		rotated, err := refresh(t.Context(), Credential{Type: CredentialOAuth, Access: "old-access", Refresh: "old-refresh"})
		if err != nil {
			t.Fatal(err)
		}
		preserved, err := refresh(t.Context(), Credential{Type: CredentialOAuth, Access: "old-access", Refresh: "keep-refresh"})
		if err != nil {
			t.Fatal(err)
		}
		if rotated.Type != CredentialOAuth || rotated.Refresh != "new-refresh" || rotated.Access != "new-access" || preserved.Refresh != "keep-refresh" || preserved.Access != "newer-access" {
			t.Fatalf("rotated=%#v preserved=%#v", rotated, preserved)
		}
		auth, ok := OAuthProviderAuth("xai")
		if !ok || auth.Name != "xAI (Grok/X subscription)" {
			t.Fatalf("OAuth metadata = %#v", auth)
		}
		resolved, err := auth.ToAuth(preserved)
		if err != nil || !reflect.DeepEqual(resolved, ModelAuth{APIKey: "newer-access"}) {
			t.Fatalf("toAuth = %#v, %v", resolved, err)
		}
	})
	// upstream: packages/ai/test/xai-oauth.test.ts:303.
	t.Run("assumes a one-hour lifetime when expires_in is missing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := upstreamXaiProvider(t, func(*http.Request) (*http.Response, error) {
				body := upstreamXaiToken()
				delete(body, "expires_in")
				return metaJSONResponse(200, body), nil
			})
			start := p.now()
			credential, err := p.RefreshTokenContext(t.Context(), OAuthCredentials{Access: "old-access", Refresh: "old-refresh"})
			if err != nil || credential.Expires != start.UnixMilli()+3600000-300000 {
				t.Fatalf("refresh = %#v, %v", credential, err)
			}
		})
	})
	// upstream: packages/ai/test/xai-oauth.test.ts:316,325.
	for _, tc := range []struct {
		name, want string
		status     int
		body       map[string]any
	}{
		{"rejects token responses with missing fields", "Invalid xAI OAuth response field: access_token", 200, func() map[string]any { body := upstreamXaiToken(); delete(body, "access_token"); return body }()},
		{"surfaces the upstream error code and description on refresh failure", "xAI OAuth token refresh failed (HTTP 400): invalid_grant: refresh token revoked", 400, map[string]any{"error": "invalid_grant", "error_description": "refresh token revoked"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := upstreamXaiProvider(t, func(*http.Request) (*http.Response, error) { return metaJSONResponse(tc.status, tc.body), nil })
			_, err := p.RefreshTokenContext(t.Context(), OAuthCredentials{Access: "old-access", Refresh: "old-refresh"})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("refresh error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestXaiRegisteredForLogin(t *testing.T) {
	p, ok := GetOAuthProvider("xai")
	if !ok {
		t.Fatal("xai OAuth provider not registered")
	}
	if p.ID() != "xai" || p.UsesCallbackServer() {
		t.Fatalf("unexpected xai provider %+v (device-code, no callback server)", p)
	}
}

func TestXaiLoginDeviceCodeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case "/device":
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"WXYZ","verification_uri":"https://x.ai/device","verification_uri_complete":"https://x.ai/device?code=WXYZ","expires_in":600,"interval":0.001}`))
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"xai-access","refresh_token":"xai-refresh","expires_in":3600}`))
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()
	p := testXaiProvider(srv.URL+"/device", srv.URL+"/token")

	var gotCode OAuthDeviceCodeInfo
	creds, err := p.Login(OAuthLoginCallbacks{
		OnDeviceCode: func(info OAuthDeviceCodeInfo) { gotCode = info },
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if creds.Access != "xai-access" || creds.Refresh != "xai-refresh" {
		t.Fatalf("creds = %+v", creds)
	}
	// expires = now(1000s) + 3600s - 5min skew, in millis.
	wantExpires := int64(1_000)*1000 + 3600*1000 - xaiRefreshSkewMs
	if creds.Expires != wantExpires {
		t.Fatalf("expires = %d, want %d", creds.Expires, wantExpires)
	}
	// The user is shown the complete verification URI when present.
	if gotCode.UserCode != "WXYZ" || gotCode.VerificationURI != "https://x.ai/device?code=WXYZ" {
		t.Fatalf("device code info = %+v", gotCode)
	}
}

func TestXaiDeviceAuthorizationFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"bad client"}`))
	}))
	defer srv.Close()
	p := testXaiProvider(srv.URL, srv.URL)
	_, err := p.Login(OAuthLoginCallbacks{})
	if err == nil || !strings.Contains(err.Error(), "device authorization failed") ||
		!strings.Contains(err.Error(), "invalid_client: bad client") {
		t.Fatalf("expected device authorization failure with detail, got %v", err)
	}
}

func TestXaiVerificationURIMustBeHTTPS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"U","verification_uri":"http://x.ai/device","expires_in":600}`))
	}))
	defer srv.Close()
	p := testXaiProvider(srv.URL, srv.URL)
	_, err := p.Login(OAuthLoginCallbacks{})
	if err == nil || !strings.Contains(err.Error(), "Untrusted verification URI") {
		t.Fatalf("expected untrusted-URI rejection for http scheme, got %v", err)
	}
}

func TestXaiCredentialsFromToken(t *testing.T) {
	p := testXaiProvider("", "")

	// Missing access token is fatal.
	if _, err := p.credentialsFromToken(xaiBody{RefreshToken: new("r")}, ""); err == nil ||
		!strings.Contains(err.Error(), "access_token") {
		t.Fatalf("expected access_token error, got %v", err)
	}

	// Fresh login must carry a refresh token.
	if _, err := p.credentialsFromToken(xaiBody{AccessToken: "a"}, ""); err == nil ||
		!strings.Contains(err.Error(), "refresh_token") {
		t.Fatalf("expected refresh_token error on fresh login, got %v", err)
	}

	// Omitted expires_in defaults to one hour.
	c, err := p.credentialsFromToken(xaiBody{AccessToken: "a", RefreshToken: new("r")}, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(1_000)*1000 + xaiDefaultTokenLifetime*1000 - xaiRefreshSkewMs; c.Expires != want {
		t.Fatalf("default expiry = %d, want %d", c.Expires, want)
	}
}

func TestXaiRefreshKeepsPreviousRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		// Non-rotating refresh: no refresh_token in the response.
		_, _ = w.Write([]byte(`{"access_token":"fresh-access","expires_in":3600}`))
	}))
	defer srv.Close()
	p := testXaiProvider(srv.URL, srv.URL)
	creds, err := p.RefreshToken(OAuthCredentials{Refresh: "kept-refresh"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if creds.Access != "fresh-access" || creds.Refresh != "kept-refresh" {
		t.Fatalf("refresh dropped the retained token: %+v", creds)
	}
}

func TestXaiPollTokenErrorMapping(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want       DeviceCodePollStatus
	}{
		{"pending", `{"error":"authorization_pending"}`, 400, DevicePollPending},
		{"slow_down", `{"error":"slow_down"}`, 400, DevicePollSlowDown},
		{"denied", `{"error":"access_denied"}`, 400, DevicePollFailed},
		{"expired", `{"error":"expired_token"}`, 400, DevicePollFailed},
		{"complete", `{"access_token":"a","refresh_token":"r","expires_in":3600}`, 200, DevicePollComplete},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("content-type", "application/json")
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			p := testXaiProvider(srv.URL, srv.URL)
			res, err := p.pollToken(context.Background(), "dc")
			if err != nil {
				t.Fatalf("poll err: %v", err)
			}
			if res.Status != c.want {
				t.Fatalf("status = %v, want %v", res.Status, c.want)
			}
		})
	}
}

type xaiCancelStep string

const (
	xaiCancelBefore    xaiCancelStep = "before"
	xaiCancelAuthorize xaiCancelStep = "authorize"
	xaiCancelFirstWait xaiCancelStep = "first-wait"
	xaiCancelPoll      xaiCancelStep = "poll"
	xaiCancelPollWait  xaiCancelStep = "poll-wait"
)

func TestXaiLoginContextCancellation(t *testing.T) {
	cases := []struct {
		step        xaiCancelStep
		requests    []string
		deviceCodes int
		elapsed     time.Duration
	}{
		{xaiCancelBefore, nil, 0, 0},
		{xaiCancelAuthorize, []string{xaiDeviceCodeURL}, 0, time.Second},
		{xaiCancelFirstWait, []string{xaiDeviceCodeURL}, 1, time.Second},
		{xaiCancelPoll, []string{xaiDeviceCodeURL, xaiTokenURL}, 1, 6 * time.Second},
		{xaiCancelPollWait, []string{xaiDeviceCodeURL, xaiTokenURL}, 1, 6 * time.Second},
	}
	for _, tc := range cases {
		t.Run(string(tc.step), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var requests []string
				provider := newXaiOAuthProvider()
				provider.client = &http.Client{Transport: metaRoundTripper(func(request *http.Request) (*http.Response, error) {
					requests = append(requests, request.URL.String())
					switch {
					case request.URL.String() == xaiDeviceCodeURL && tc.step == xaiCancelAuthorize,
						request.URL.String() == xaiTokenURL && tc.step == xaiCancelPoll:
						return abortInFlight(request, cancel)
					case request.URL.String() == xaiDeviceCodeURL:
						return metaJSONResponse(200, map[string]any{
							"device_code": "device-code", "user_code": "ABCD-1234",
							"verification_uri": "https://accounts.x.ai/oauth2/device", "expires_in": 900, "interval": 5,
						}), nil
					case tc.step == xaiCancelPollWait:
						time.AfterFunc(time.Second, cancel)
						return metaJSONResponse(400, map[string]any{"error": "authorization_pending"}), nil
					default:
						return metaJSONResponse(200, map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600}), nil
					}
				})}
				if tc.step == xaiCancelBefore {
					cancel()
				}
				deviceCodes := 0
				start := time.Now()
				credentials, err := provider.LoginContext(ctx, OAuthLoginCallbacks{OnDeviceCode: func(OAuthDeviceCodeInfo) {
					deviceCodes++
					if tc.step == xaiCancelFirstWait {
						time.AfterFunc(time.Second, cancel)
					}
				}})
				if err == nil || err.Error() != deviceCodeCancelMessage || !reflect.DeepEqual(credentials, OAuthCredentials{}) {
					t.Fatalf("LoginContext = %#v, %v; want %q", credentials, err, deviceCodeCancelMessage)
				}
				if elapsed := time.Since(start); elapsed != tc.elapsed {
					t.Fatalf("returned after %v, want %v", elapsed, tc.elapsed)
				}
				requestCount := len(requests)
				time.Sleep(10 * time.Minute)
				if len(requests) != requestCount || !slices.Equal(requests, tc.requests) {
					t.Fatalf("requests = %v, want %v", requests, tc.requests)
				}
				if deviceCodes != tc.deviceCodes {
					t.Fatalf("device code notifications = %d, want %d", deviceCodes, tc.deviceCodes)
				}
			})
		})
	}
}

func TestXaiRefreshTokenContextCancellation(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		t.Run(fmt.Sprint(inFlight), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				requests := 0
				provider := newXaiOAuthProvider()
				provider.client = &http.Client{Transport: metaRoundTripper(func(request *http.Request) (*http.Response, error) {
					requests++
					return abortInFlight(request, cancel)
				})}
				if !inFlight {
					cancel()
				}
				start := time.Now()
				credentials, err := provider.RefreshTokenContext(ctx, OAuthCredentials{Refresh: "refresh"})
				wantRequests, wantElapsed := 0, time.Duration(0)
				if inFlight {
					wantRequests, wantElapsed = 1, time.Second
				}
				if err == nil || !reflect.DeepEqual(credentials, OAuthCredentials{}) || requests != wantRequests || time.Since(start) != wantElapsed {
					t.Fatalf("in-flight=%v: refresh = %#v, %v after %v with %d requests", inFlight, credentials, err, time.Since(start), requests)
				}
			})
		})
	}
}
