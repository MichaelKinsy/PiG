package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

func isolateAnthropicCallbackHost(t *testing.T) string {
	t.Helper()
	host := anthropicCallbackTestHost()
	t.Setenv("PI_OAUTH_CALLBACK_HOST", host)
	return host
}

// anthropicCallbackTestHost returns a per-process loopback address so concurrent
// test processes do not contend for Anthropic's fixed callback port. Platforms that
// only assign 127.0.0.1 on the loopback interface (macOS) fall back to it.
func anthropicCallbackTestHost() string {
	pid := os.Getpid()
	host := fmt.Sprintf("127.%d.%d.%d", (pid>>16)&255, (pid>>8)&255, pid&255)
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return "127.0.0.1"
	}
	_ = listener.Close()
	return host
}

func mockAnthropicOAuthToken(t *testing.T, response string, check func(*http.Request, map[string]string)) *int {
	t.Helper()
	isolateAnthropicCallbackHost(t)
	calls := new(int)
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: responsesTestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		*calls++
		if request.URL.String() != "https://platform.claude.com/v1/oauth/token" || request.Method != http.MethodPost {
			t.Errorf("request=%s %s", request.Method, request.URL)
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		check(request, body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	return calls
}

func TestAnthropicUpstreamOAuth(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/anthropic-oauth.test.ts:41
	t.Run("keeps the localhost redirect_uri for manual callback login", func(t *testing.T) {
		calls := mockAnthropicOAuthToken(t, `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600}`, func(_ *http.Request, body map[string]string) {
			if body["grant_type"] != "authorization_code" || body["code"] != "manual-code" || body["redirect_uri"] != "http://localhost:53692/callback" {
				t.Errorf("body=%v", body)
			}
		})
		authURL := ""
		credential, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{OnAuth: func(info OAuthAuthInfo) { authURL = info.URL }, OnManualCodeInput: func() (string, error) {
			parsed, err := url.Parse(authURL)
			if err != nil {
				return "", err
			}
			state, redirect := parsed.Query().Get("state"), parsed.Query().Get("redirect_uri")
			if state == "" || redirect == "" {
				t.Error("missing state or redirect_uri")
			}
			return redirect + "?code=manual-code&state=" + url.QueryEscape(state), nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		if credential.Access != "access-token" || credential.Refresh != "refresh-token" || *calls != 1 {
			t.Fatalf("credential=%+v requests=%d", credential, *calls)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-oauth.test.ts:78
	t.Run("omits scope from refresh token requests", func(t *testing.T) {
		calls := mockAnthropicOAuthToken(t, `{"access_token":"new-access-token","refresh_token":"new-refresh-token","expires_in":3600}`, func(_ *http.Request, body map[string]string) {
			if body["grant_type"] != "refresh_token" || body["client_id"] == "" || body["refresh_token"] != "refresh-token" {
				t.Errorf("body=%v", body)
			}
			if _, ok := body["scope"]; ok {
				t.Error("refresh request contains scope")
			}
		})
		credential, err := (AnthropicOAuthProvider{}).RefreshTokenContext(t.Context(), OAuthCredentials{Access: "old-access-token", Refresh: "refresh-token", Expires: 0})
		if err != nil {
			t.Fatal(err)
		}
		if credential.Access != "new-access-token" || credential.Refresh != "new-refresh-token" || *calls != 1 {
			t.Fatalf("credential=%+v requests=%d", credential, *calls)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-oauth.test.ts:110
	t.Run("anthropicOAuth.login resolves through the manual_code prompt and aborts it after settling", func(t *testing.T) {
		mockAnthropicOAuthToken(t, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`, func(*http.Request, map[string]string) {})
		var manualSignal context.Context
		authEvent := false
		credential, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{OnAuth: func(info OAuthAuthInfo) { authEvent = info.URL != "" }, OnManualCodeInput: func() (string, error) { return "the-code", nil }, OnManualCodeInputContext: func(ctx context.Context) (string, error) { manualSignal = ctx; return "the-code", nil }})
		if err != nil {
			t.Fatal(err)
		}
		if credential.Access != "access" || !authEvent {
			t.Fatalf("credential=%+v authEvent=%t", credential, authEvent)
		}
		if manualSignal == nil {
			t.Fatal("manual_code contextual prompt was not invoked")
		}
		if manualSignal.Err() != context.Canceled {
			t.Fatalf("manual prompt signal not aborted: %v", manualSignal.Err())
		}
	})
}
