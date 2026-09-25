package ai

// Ports .upstream/current/packages/ai/test/meta-oauth.test.ts.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

const metaDay = 24 * time.Hour

type metaRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip metaRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func metaJSONResponse(status int, body any) *http.Response {
	encoded, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(encoded))),
	}
}

func testMetaProvider(roundTrip metaRoundTripper) metaOAuthProvider {
	provider := newMetaOAuthProvider()
	provider.client = &http.Client{Transport: roundTrip}
	return provider
}

func readMetaForm(t *testing.T, request *http.Request) url.Values {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func TestMetaOAuthRegisteredForLogin(t *testing.T) {
	provider, ok := GetOAuthProvider("meta")
	if !ok {
		t.Fatal("meta OAuth provider not registered")
	}
	if provider.Name() != "Meta (Muse subscription)" || provider.UsesCallbackServer() {
		t.Fatalf("meta provider = %q callback=%v", provider.Name(), provider.UsesCallbackServer())
	}
}

func TestMetaOAuthLogsInWithDeviceFlowAndMintsModelAPIKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var mu sync.Mutex
		type pollReply struct {
			status int
			body   map[string]any
		}
		pollReplies := []pollReply{
			{400, map[string]any{"error": "authorization_pending"}},
			{200, map[string]any{"access_token": "identity-token", "token_type": "Bearer"}},
		}
		provider := testMetaProvider(func(request *http.Request) (*http.Response, error) {
			mu.Lock()
			defer mu.Unlock()
			if request.Method != http.MethodPost {
				t.Errorf("%s method = %s", request.URL, request.Method)
			}
			switch request.URL.String() {
			case metaDeviceAuthorizationURL:
				if got := readMetaForm(t, request).Get("client_id"); got != metaClientID {
					t.Errorf("client_id = %q", got)
				}
				return metaJSONResponse(200, map[string]any{
					"device_code": "device-code-123", "user_code": "ABCD-1234",
					"verification_uri":          "https://auth.meta.com/oauth/device/",
					"verification_uri_complete": "https://auth.meta.com/oauth/device/?code=ABCD-1234",
					"interval":                  5, "expires_in": 600,
				}), nil
			case metaDeviceTokenURL:
				form := readMetaForm(t, request)
				if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || form.Get("client_id") != metaClientID || form.Get("device_code") != "device-code-123" {
					t.Errorf("token form = %v", form)
				}
				if len(pollReplies) == 0 {
					t.Fatal("unexpected extra token poll")
				}
				next := pollReplies[0]
				pollReplies = pollReplies[1:]
				return metaJSONResponse(next.status, next.body), nil
			case metaAPIKeyMintURL:
				if got := request.Header.Get("Authorization"); got != "Bearer identity-token" {
					t.Errorf("mint Authorization = %q", got)
				}
				return metaJSONResponse(200, map[string]any{"api_key": "LLM|minted-key"}), nil
			}
			t.Fatalf("unexpected fetch URL: %s", request.URL)
			return nil, nil
		})

		var events []OAuthDeviceCodeInfo
		var progress []string
		credentials, err := provider.Login(OAuthLoginCallbacks{
			OnDeviceCode: func(info OAuthDeviceCodeInfo) { events = append(events, info) },
			OnProgress:   func(message string) { progress = append(progress, message) },
		})
		if err != nil {
			t.Fatalf("Login: %v", err)
		}
		want := OAuthDeviceCodeInfo{UserCode: "ABCD-1234", VerificationURI: "https://auth.meta.com/oauth/device/?code=ABCD-1234", IntervalSeconds: 5, ExpiresInSeconds: 600}
		if len(events) != 1 || events[0] != want {
			t.Fatalf("device code events = %#v", events)
		}
		if len(progress) != 1 || progress[0] != "Enabling Meta Model API access..." {
			t.Fatalf("progress = %#v", progress)
		}
		// The first poll waits one interval (5s) and is pending; the second
		// poll completes after another interval.
		wantCredentials := OAuthCredentials{Refresh: "identity-token", Access: "LLM|minted-key", Expires: start.Add(10*time.Second + metaDay).UnixMilli()}
		if credentials != wantCredentials {
			t.Fatalf("credentials = %#v, want %#v", credentials, wantCredentials)
		}
	})
}

func TestMetaOAuthRefreshReMintsKeyFromStoredIdentityToken(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	provider := testMetaProvider(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != metaAPIKeyMintURL || request.Header.Get("Authorization") != "Bearer identity-token" {
			t.Errorf("refresh request = %s %v", request.URL, request.Header)
		}
		if request.Header.Get("x-api-version") != "1.0.0" || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("mint headers = %v", request.Header)
		}
		return metaJSONResponse(200, map[string]any{"api_key": "LLM|fresh-key"}), nil
	})
	provider.now = func() time.Time { return now }

	got, err := provider.RefreshToken(OAuthCredentials{Refresh: "identity-token", Access: "LLM|old-key", Expires: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := OAuthCredentials{Refresh: "identity-token", Access: "LLM|fresh-key", Expires: now.Add(metaDay).UnixMilli()}
	if got != want {
		t.Fatalf("refreshed = %#v, want %#v", got, want)
	}
}

func TestMetaOAuthReportsSetupURLWhenNoKeyIssued(t *testing.T) {
	provider := testMetaProvider(func(*http.Request) (*http.Response, error) {
		return metaJSONResponse(200, map[string]any{"require_payment": true, "action_url": "https://dev.meta.ai/billing"}), nil
	})
	_, err := provider.RefreshToken(OAuthCredentials{Refresh: "identity-token", Expires: 1})
	if err == nil || !strings.Contains(err.Error(), "Complete setup at https://dev.meta.ai/billing") {
		t.Fatalf("err = %v", err)
	}
}

func TestMetaOAuthUsesMintedKeyAsRequestAPIKey(t *testing.T) {
	if got := newMetaOAuthProvider().GetAPIKey(OAuthCredentials{Refresh: "identity-token", Access: "LLM|key", Expires: 1}); got != "LLM|key" {
		t.Fatalf("api key = %q", got)
	}
}

func TestMetaOAuthRefreshRejectsDeadSession(t *testing.T) {
	provider := testMetaProvider(func(*http.Request) (*http.Response, error) {
		return metaJSONResponse(401, map[string]any{"detail": "token expired"}), nil
	})
	_, err := provider.RefreshToken(OAuthCredentials{Refresh: "identity-token", Expires: 1})
	want := "Meta session expired (status 401). Run `/login meta` to sign in again.: token expired"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestMetaOAuthRejectsUntrustedVerificationURI(t *testing.T) {
	provider := testMetaProvider(func(*http.Request) (*http.Response, error) {
		return metaJSONResponse(200, map[string]any{"device_code": "d", "user_code": "u", "verification_uri": "javascript:alert(1)"}), nil
	})
	_, err := provider.Login(OAuthLoginCallbacks{})
	if err == nil || !strings.Contains(err.Error(), "Invalid Meta device authorization response") {
		t.Fatalf("err = %v", err)
	}
}

// metaCancelStep names where a Meta login's owner context is cancelled.
type metaCancelStep string

const (
	metaCancelBefore       metaCancelStep = "before"
	metaCancelAuthorize    metaCancelStep = "authorize"
	metaCancelFirstWait    metaCancelStep = "first-wait"
	metaCancelPoll         metaCancelStep = "poll"
	metaCancelPollWait     metaCancelStep = "poll-wait"
	metaCancelMint         metaCancelStep = "mint"
	metaCancelMintResponse metaCancelStep = "mint-response"
)

type metaCancelRun struct {
	requests    []string
	deviceCodes int
	progress    int
	elapsed     time.Duration
	credentials OAuthCredentials
	err         error
}

// runMetaLoginCancelledAt drives LoginContext and cancels its owner at step.
func runMetaLoginCancelledAt(step metaCancelStep) metaCancelRun {
	var run metaCancelRun
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := testMetaProvider(func(request *http.Request) (*http.Response, error) {
		run.requests = append(run.requests, request.URL.String())
		switch {
		case request.URL.String() == metaDeviceAuthorizationURL && step == metaCancelAuthorize,
			request.URL.String() == metaDeviceTokenURL && step == metaCancelPoll,
			request.URL.String() == metaAPIKeyMintURL && step == metaCancelMint:
			return abortInFlight(request, cancel)
		case request.URL.String() == metaDeviceAuthorizationURL:
			return metaJSONResponse(200, map[string]any{
				"device_code": "device-code", "user_code": "ABCD-1234",
				"verification_uri": "https://auth.meta.com/oauth/device/", "interval": 5, "expires_in": 600,
			}), nil
		case request.URL.String() == metaDeviceTokenURL && step == metaCancelPollWait:
			time.AfterFunc(time.Second, cancel)
			return metaJSONResponse(400, map[string]any{"error": "authorization_pending"}), nil
		case request.URL.String() == metaDeviceTokenURL:
			return metaJSONResponse(200, map[string]any{"access_token": "identity-token"}), nil
		case step == metaCancelMintResponse:
			// Cancelled after the response arrived: upstream's catch still
			// reports the cancellation rather than the mint failure.
			cancel()
			return metaJSONResponse(401, map[string]any{"detail": "token expired"}), nil
		}
		return metaJSONResponse(200, map[string]any{"api_key": "LLM|minted-key"}), nil
	})
	if step == metaCancelBefore {
		cancel()
	}
	start := time.Now()
	run.credentials, run.err = provider.LoginContext(ctx, OAuthLoginCallbacks{
		OnDeviceCode: func(OAuthDeviceCodeInfo) {
			run.deviceCodes++
			if step == metaCancelFirstWait {
				time.AfterFunc(time.Second, cancel)
			}
		},
		OnProgress: func(string) { run.progress++ },
	})
	run.elapsed = time.Since(start)
	return run
}

// TestMetaOAuthLoginContextCancellation proves each step of loginMeta honors
// the owner's cancellation: Login returns "Login cancelled" at the moment of
// cancellation, with no later poll, notification, or credential.
func TestMetaOAuthLoginContextCancellation(t *testing.T) {
	authorize, token, mint := metaDeviceAuthorizationURL, metaDeviceTokenURL, metaAPIKeyMintURL
	cases := []struct {
		step                  metaCancelStep
		requests              []string
		deviceCodes, progress int
		elapsed               time.Duration
	}{
		{metaCancelBefore, nil, 0, 0, 0},
		{metaCancelAuthorize, []string{authorize}, 0, 0, time.Second},
		{metaCancelFirstWait, []string{authorize}, 1, 0, time.Second},
		{metaCancelPoll, []string{authorize, token}, 1, 0, 6 * time.Second},
		{metaCancelPollWait, []string{authorize, token}, 1, 0, 6 * time.Second},
		{metaCancelMint, []string{authorize, token, mint}, 1, 1, 6 * time.Second},
		{metaCancelMintResponse, []string{authorize, token, mint}, 1, 1, 5 * time.Second},
	}
	for _, tc := range cases {
		t.Run(string(tc.step), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				run := runMetaLoginCancelledAt(tc.step)
				if run.err == nil || run.err.Error() != deviceCodeCancelMessage || run.credentials != (OAuthCredentials{}) {
					t.Fatalf("LoginContext = %#v, %v; want %q", run.credentials, run.err, deviceCodeCancelMessage)
				}
				if run.elapsed != tc.elapsed {
					t.Fatalf("returned after %v, want %v", run.elapsed, tc.elapsed)
				}
				requests := len(run.requests)
				time.Sleep(10 * time.Minute)
				if len(run.requests) != requests || !slices.Equal(run.requests, tc.requests) {
					t.Fatalf("requests = %v, want %v", run.requests, tc.requests)
				}
				if run.deviceCodes != tc.deviceCodes || run.progress != tc.progress {
					t.Fatalf("notifications: device codes %d progress %d, want %d and %d", run.deviceCodes, run.progress, tc.deviceCodes, tc.progress)
				}
			})
		})
	}
}

func TestMetaOAuthRefreshTokenContextCancellation(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requests := 0
			provider := testMetaProvider(func(request *http.Request) (*http.Response, error) {
				requests++
				return abortInFlight(request, cancel)
			})
			if !inFlight {
				cancel()
			}
			start := time.Now()
			got, err := provider.RefreshTokenContext(ctx, OAuthCredentials{Refresh: "identity-token", Access: "LLM|old-key", Expires: 1})
			wantRequests, wantElapsed := 0, time.Duration(0)
			if inFlight {
				wantRequests, wantElapsed = 1, time.Second
			}
			if err == nil || got != (OAuthCredentials{}) || requests != wantRequests || time.Since(start) != wantElapsed {
				t.Fatalf("in-flight=%v: refresh = %#v, %v after %v with %d requests", inFlight, got, err, time.Since(start), requests)
			}
		})
	}
}
