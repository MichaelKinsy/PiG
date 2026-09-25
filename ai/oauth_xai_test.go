package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func testXaiProvider(deviceURL, tokenURL string) xaiOAuthProvider {
	return xaiOAuthProvider{
		client:    http.DefaultClient,
		deviceURL: deviceURL,
		tokenURL:  tokenURL,
		now:       func() time.Time { return time.Unix(1_000, 0) },
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
				if err == nil || err.Error() != deviceCodeCancelMessage || credentials != (OAuthCredentials{}) {
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
				if err == nil || credentials != (OAuthCredentials{}) || requests != wantRequests || time.Since(start) != wantElapsed {
					t.Fatalf("in-flight=%v: refresh = %#v, %v after %v with %d requests", inFlight, credentials, err, time.Since(start), requests)
				}
			})
		})
	}
}
