package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
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

// selectAnthropicBrowserLogin answers the login method prompt with browser login, as the upstream tests' prompt
// callbacks do (.upstream/v1.0.0/packages/ai/test/anthropic-oauth.test.ts:65, 195, 234).
func selectAnthropicBrowserLogin(OAuthSelectPrompt) (string, error) { return "browser", nil }

func TestAnthropicUpstreamOAuth(t *testing.T) {
	// .upstream/v1.0.0/packages/ai/test/anthropic-oauth.test.ts:42
	t.Run("keeps the localhost redirect_uri for manual callback login", func(t *testing.T) {
		calls := mockAnthropicOAuthToken(t, `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600}`, func(_ *http.Request, body map[string]string) {
			if body["grant_type"] != "authorization_code" || body["code"] != "manual-code" || body["redirect_uri"] != "http://localhost:53692/callback" {
				t.Errorf("body=%v", body)
			}
		})
		authURL := ""
		credential, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{OnAuth: func(info OAuthAuthInfo) { authURL = info.URL }, OnSelect: selectAnthropicBrowserLogin, OnManualCodeInput: func() (string, error) {
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
	// .upstream/v1.0.0/packages/ai/test/anthropic-oauth.test.ts:80
	t.Run("offers browser login first and uses the selected Anthropic copy code flow", func(t *testing.T) {
		var selectPrompts []OAuthSelectPrompt
		authURL := ""
		authState := func() string {
			parsed, err := url.Parse(authURL)
			if err != nil {
				t.Error(err)
				return ""
			}
			return parsed.Query().Get("state")
		}
		calls := mockAnthropicOAuthToken(t, `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600}`, func(_ *http.Request, body map[string]string) {
			if body["grant_type"] != "authorization_code" || body["code"] != "copied-code" || body["state"] != authState() || body["redirect_uri"] != "https://platform.claude.com/oauth/code/callback" {
				t.Errorf("body=%v", body)
			}
		})
		manualCode := func() (string, error) { return "copied-code#" + authState(), nil }
		credential, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{
			OnAuth: func(info OAuthAuthInfo) { authURL = info.URL },
			OnSelect: func(prompt OAuthSelectPrompt) (string, error) {
				selectPrompts = append(selectPrompts, prompt)
				return "copy_code", nil
			},
			OnManualCodeInput:        manualCode,
			OnManualCodeInputContext: func(context.Context) (string, error) { return manualCode() },
		})
		if err != nil {
			t.Fatal(err)
		}
		if credential.Access != "access-token" || credential.Refresh != "refresh-token" {
			t.Fatalf("credential=%+v", credential)
		}
		parsed, err := url.Parse(authURL)
		if err != nil {
			t.Fatal(err)
		}
		if redirect := parsed.Query().Get("redirect_uri"); redirect != "https://platform.claude.com/oauth/code/callback" {
			t.Errorf("auth URL redirect_uri=%q", redirect)
		}
		if *calls != 1 {
			t.Errorf("token requests=%d, want 1", *calls)
		}
		want := []OAuthSelectPrompt{{
			Message: "Select Anthropic login method:",
			Options: []OAuthSelectOption{
				{ID: "browser", Label: "Browser login (default)"},
				{ID: "copy_code", Label: "Copy code login (headless)"},
			},
		}}
		if !reflect.DeepEqual(selectPrompts, want) {
			t.Errorf("select prompts=%+v, want %+v", selectPrompts, want)
		}
	})
	// .upstream/v1.0.0/packages/ai/test/anthropic-oauth.test.ts:132
	t.Run("cancels when Anthropic login method selection is cancelled", func(t *testing.T) {
		isolateAnthropicCallbackHost(t)
		cancelled := func() (string, error) { return "", errors.New("Login cancelled") }
		_, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{
			OnAuth:                   func(OAuthAuthInfo) {},
			OnSelect:                 func(OAuthSelectPrompt) (string, error) { return cancelled() },
			OnManualCodeInput:        cancelled,
			OnManualCodeInputContext: func(context.Context) (string, error) { return cancelled() },
		})
		if err == nil || !strings.Contains(err.Error(), "Login cancelled") {
			t.Fatalf("err=%v, want Login cancelled", err)
		}
	})
	// .upstream/v1.0.0/packages/ai/test/anthropic-oauth.test.ts:144
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
	// .upstream/v1.0.0/packages/ai/test/anthropic-oauth.test.ts:176
	t.Run("anthropicOAuth.login resolves through the manual_code prompt and aborts it after settling", func(t *testing.T) {
		mockAnthropicOAuthToken(t, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`, func(*http.Request, map[string]string) {})
		var manualSignal context.Context
		authEvent := false
		credential, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{OnAuth: func(info OAuthAuthInfo) { authEvent = info.URL != "" }, OnSelect: selectAnthropicBrowserLogin, OnManualCodeInput: func() (string, error) { return "the-code", nil }, OnManualCodeInputContext: func(ctx context.Context) (string, error) { manualSignal = ctx; return "the-code", nil }})
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
	// anthropic-oauth.test.ts "completes login through the browser callback and shows the sign-in page" (1.1.0: the exchange carries the redirect URI the authorization URL named).
	t.Run("completes login through the browser callback and shows the sign-in page", func(t *testing.T) {
		login := loginThroughAnthropicBrowserCallback(t)
		if login.credential.Access != "access" || login.exchangedCode != "browser-code" || login.exchangedRedirectURI != login.redirectURI {
			t.Fatalf("credential=%+v code=%q exchanged redirect_uri=%q authorization redirect_uri=%q", login.credential, login.exchangedCode, login.exchangedRedirectURI, login.redirectURI)
		}
		if login.pageStatus != 200 || !strings.Contains(login.pageBody, "Signed in to Anthropic.") {
			t.Fatalf("callback page=%d %q", login.pageStatus, login.pageBody)
		}
	})
	// anthropic-oauth.test.ts "falls back to a free callback port when the preferred port cannot be bound" (#10571).
	t.Run("falls back to a free callback port when the preferred port cannot be bound", func(t *testing.T) {
		blocker, err := net.Listen("tcp", net.JoinHostPort(isolateAnthropicCallbackHost(t), "53692"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blocker.Close() }()
		login := loginThroughAnthropicBrowserCallback(t)
		redirect, err := url.Parse(login.redirectURI)
		if err != nil {
			t.Fatal(err)
		}
		if redirect.Hostname() != "localhost" || redirect.Path != "/callback" || redirect.Port() == "53692" || redirect.Port() == "" {
			t.Fatalf("redirect_uri=%q, want localhost on a free port with /callback", login.redirectURI)
		}
		if login.credential.Access != "access" || login.exchangedRedirectURI != login.redirectURI || login.pageStatus != 200 {
			t.Fatalf("credential=%+v exchanged redirect_uri=%q page=%d", login.credential, login.exchangedRedirectURI, login.pageStatus)
		}
	})
}

type anthropicBrowserLogin struct {
	credential           OAuthCredentials
	redirectURI          string
	exchangedCode        string
	exchangedRedirectURI string
	pageStatus           int
	pageBody             string
}

// loginThroughAnthropicBrowserCallback is loginThroughBrowserCallback of anthropic-oauth.test.ts: the browser follows the authorization URL's redirect_uri to the loopback callback.
func loginThroughAnthropicBrowserCallback(t *testing.T) anthropicBrowserLogin {
	t.Helper()
	var login anthropicBrowserLogin
	mockAnthropicOAuthToken(t, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`, func(_ *http.Request, body map[string]string) {
		login.exchangedCode, login.exchangedRedirectURI = body["code"], body["redirect_uri"]
	})
	host := os.Getenv("PI_OAUTH_CALLBACK_HOST")
	type page struct {
		status int
		body   string
	}
	callbackPage := make(chan page, 1)
	credential, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{
		OnSelect: selectAnthropicBrowserLogin,
		OnAuth: func(info OAuthAuthInfo) {
			parsed, err := url.Parse(info.URL)
			if err != nil {
				t.Error(err)
				return
			}
			login.redirectURI = parsed.Query().Get("redirect_uri")
			callback, err := url.Parse(login.redirectURI)
			if err != nil {
				t.Error(err)
				return
			}
			callback.Host = net.JoinHostPort(host, callback.Port())
			callback.RawQuery = url.Values{"code": {"browser-code"}, "state": {parsed.Query().Get("state")}}.Encode()
			go func() {
				response, err := oauthNativeClient.Get(callback.String())
				if err != nil {
					t.Error(err)
					callbackPage <- page{}
					return
				}
				defer func() { _ = response.Body.Close() }()
				body, _ := io.ReadAll(response.Body)
				callbackPage <- page{response.StatusCode, string(body)}
			}()
		},
		OnManualCodeInputContext: func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", errors.New("aborted")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	login.credential = credential
	response := <-callbackPage
	login.pageStatus, login.pageBody = response.status, response.body
	return login
}

// Implementation-derived cases for the Pi 1.0.0 Anthropic login method prompt and copy code flow, which the upstream
// tests do not reach (.upstream/v1.0.0/packages/ai/src/auth/oauth/anthropic.ts:191-226, 273-291).
func TestAnthropicCopyCodeLoginImplementation(t *testing.T) {
	copyCode := func(OAuthSelectPrompt) (string, error) { return "copy_code", nil }
	// anthropic.ts:286-288: any answer other than browser or copy_code fails before a flow starts.
	t.Run("rejects an unknown login method", func(t *testing.T) {
		calls := mockAnthropicOAuthToken(t, `{}`, func(*http.Request, map[string]string) {})
		authURL := ""
		_, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{
			OnAuth:            func(info OAuthAuthInfo) { authURL = info.URL },
			OnSelect:          func(OAuthSelectPrompt) (string, error) { return "other", nil },
			OnManualCodeInput: func() (string, error) { return "unused", nil },
		})
		if err == nil || err.Error() != "Unknown Anthropic login method: other" {
			t.Fatalf("err=%v, want Unknown Anthropic login method: other", err)
		}
		if authURL != "" || *calls != 0 {
			t.Fatalf("auth URL=%q token requests=%d, want no flow", authURL, *calls)
		}
	})
	// anthropic.ts:203-207: the copy code flow tells the user to paste the code Anthropic shows.
	t.Run("copy code login shows its own instructions", func(t *testing.T) {
		mockAnthropicOAuthToken(t, `{"access_token":"a","refresh_token":"r","expires_in":3600}`, func(*http.Request, map[string]string) {})
		var info OAuthAuthInfo
		manual := func() (string, error) { return "copied-code", nil }
		if _, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{
			OnAuth: func(got OAuthAuthInfo) { info = got }, OnSelect: copyCode,
			OnManualCodeInput: manual, OnManualCodeInputContext: func(context.Context) (string, error) { return manual() },
		}); err != nil {
			t.Fatal(err)
		}
		if info.Instructions != "Complete login in your browser, then copy the code Anthropic shows and paste it here." {
			t.Fatalf("instructions=%q", info.Instructions)
		}
	})
	// anthropic.ts:216: a pasted state that differs from the PKCE verifier fails before the token exchange.
	t.Run("copy code login rejects a mismatched state", func(t *testing.T) {
		calls := mockAnthropicOAuthToken(t, `{}`, func(*http.Request, map[string]string) {})
		manual := func() (string, error) { return "copied-code#other-state", nil }
		_, err := (AnthropicOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{
			OnAuth: func(OAuthAuthInfo) {}, OnSelect: copyCode,
			OnManualCodeInput: manual, OnManualCodeInputContext: func(context.Context) (string, error) { return manual() },
		})
		if err == nil || err.Error() != "OAuth state mismatch" || *calls != 0 {
			t.Fatalf("err=%v token requests=%d, want OAuth state mismatch before any request", err, *calls)
		}
	})
}
