package ai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type codexRoundTripper func(*http.Request) (*http.Response, error)

func (f codexRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func withMockCodexClient(t *testing.T, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: codexRoundTripper(handler)}
	t.Cleanup(func() { http.DefaultClient = old })
}

func codexJSONResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func TestStartCodexDeviceAuth_Success(t *testing.T) {
	withMockCodexClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/accounts/deviceauth/usercode" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), codexClientID) {
			t.Errorf("client_id missing from body %q", body)
		}
		return codexJSONResp(200, `{"device_auth_id":"dev-1","user_code":"ABCD-1234","interval":3}`), nil
	})
	dev, err := startCodexDeviceAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if dev.deviceAuthID != "dev-1" || dev.userCode != "ABCD-1234" || dev.intervalSeconds != 3 {
		t.Fatalf("parsed wrong: %+v", dev)
	}
}

func TestStartCodexDeviceAuth_IntervalAsString(t *testing.T) {
	withMockCodexClient(t, func(_ *http.Request) (*http.Response, error) {
		return codexJSONResp(200, `{"device_auth_id":"d","user_code":"u","interval":"5"}`), nil
	})
	dev, err := startCodexDeviceAuth(context.Background())
	if err != nil || dev.intervalSeconds != 5 {
		t.Fatalf("interval string not parsed: %+v err=%v", dev, err)
	}
}

func TestStartCodexDeviceAuth_404NotEnabled(t *testing.T) {
	withMockCodexClient(t, func(_ *http.Request) (*http.Response, error) {
		return codexJSONResp(404, ``), nil
	})
	_, err := startCodexDeviceAuth(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not enabled for this server") {
		t.Fatalf("err = %v, want not-enabled message", err)
	}
}

func TestStartCodexDeviceAuth_InvalidResponse(t *testing.T) {
	withMockCodexClient(t, func(_ *http.Request) (*http.Response, error) {
		return codexJSONResp(200, `{"device_auth_id":"d"}`), nil // missing user_code + interval
	})
	_, err := startCodexDeviceAuth(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Invalid OpenAI Codex device code response") {
		t.Fatalf("err = %v, want invalid-response", err)
	}
}

func TestPollCodexDeviceAuth_403PendingThenComplete(t *testing.T) {
	calls := 0
	withMockCodexClient(t, func(_ *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return codexJSONResp(403, ``), nil // pending
		}
		return codexJSONResp(200, `{"authorization_code":"auth-9","code_verifier":"ver-9"}`), nil
	})
	tok, err := pollCodexDeviceAuth(context.Background(), codexDeviceAuthInfo{deviceAuthID: "d", userCode: "u", intervalSeconds: 0})
	if err != nil {
		t.Fatal(err)
	}
	if tok.authorizationCode != "auth-9" || tok.codeVerifier != "ver-9" {
		t.Fatalf("token wrong: %+v", tok)
	}
}

func TestPollCodexDeviceAuth_ErrorCodePending(t *testing.T) {
	calls := 0
	withMockCodexClient(t, func(_ *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return codexJSONResp(400, `{"error":{"code":"deviceauth_authorization_pending"}}`), nil
		}
		return codexJSONResp(200, `{"authorization_code":"a","code_verifier":"v"}`), nil
	})
	tok, err := pollCodexDeviceAuth(context.Background(), codexDeviceAuthInfo{deviceAuthID: "d", userCode: "u", intervalSeconds: 0})
	if err != nil || tok.authorizationCode != "a" {
		t.Fatalf("tok=%+v err=%v", tok, err)
	}
}

func TestPollCodexDeviceAuth_HardFailure(t *testing.T) {
	withMockCodexClient(t, func(_ *http.Request) (*http.Response, error) {
		return codexJSONResp(400, `{"error":"access_denied"}`), nil
	})
	_, err := pollCodexDeviceAuth(context.Background(), codexDeviceAuthInfo{deviceAuthID: "d", userCode: "u", intervalSeconds: 2})
	if err == nil || !strings.Contains(err.Error(), "device auth failed with status 400") {
		t.Fatalf("err = %v, want hard failure", err)
	}
}

func TestLoginOpenAICodexDeviceCode_EndToEnd(t *testing.T) {
	var gotInfo OAuthDeviceCodeInfo
	withMockCodexClient(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			return codexJSONResp(200, `{"device_auth_id":"d","user_code":"WXYZ-7","interval":0}`), nil
		case "/api/accounts/deviceauth/token":
			return codexJSONResp(200, `{"authorization_code":"ac","code_verifier":"cv"}`), nil
		case "/oauth/token":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "grant_type=authorization_code") {
				t.Errorf("token exchange grant_type missing: %q", body)
			}
			if !strings.Contains(string(body), "code_verifier=cv") {
				t.Errorf("token exchange code_verifier missing: %q", body)
			}
			return codexJSONResp(200, `{"access_token":"acc","refresh_token":"ref","expires_in":3600}`), nil
		}
		t.Fatalf("unexpected path %q", r.URL.Path)
		return nil, nil
	})

	creds, err := LoginOpenAICodexDeviceCode(context.Background(), func(info OAuthDeviceCodeInfo) {
		gotInfo = info
	})
	if err != nil {
		t.Fatal(err)
	}
	if creds.Access != "acc" || creds.Refresh != "ref" {
		t.Fatalf("creds wrong: %+v", creds)
	}
	if gotInfo.UserCode != "WXYZ-7" || gotInfo.VerificationURI != codexDeviceVerificationURI {
		t.Fatalf("onDeviceCode info wrong: %+v", gotInfo)
	}
	if gotInfo.ExpiresInSeconds != codexDeviceCodeTimeoutSeconds {
		t.Fatalf("expiresInSeconds = %v, want %d", gotInfo.ExpiresInSeconds, codexDeviceCodeTimeoutSeconds)
	}
}

func TestCodexProvider_Login_DeviceCodeDispatch(t *testing.T) {
	withMockCodexClient(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			return codexJSONResp(200, `{"device_auth_id":"d","user_code":"U","interval":0}`), nil
		case "/api/accounts/deviceauth/token":
			return codexJSONResp(200, `{"authorization_code":"ac","code_verifier":"cv"}`), nil
		case "/oauth/token":
			return codexJSONResp(200, `{"access_token":"acc","refresh_token":"ref","expires_in":3600}`), nil
		}
		t.Fatalf("unexpected path %q", r.URL.Path)
		return nil, nil
	})

	var selectMsg string
	deviceShown := false
	creds, err := CodexOAuthProvider{}.Login(OAuthLoginCallbacks{
		OnSelect: func(p OAuthSelectPrompt) (string, error) {
			selectMsg = p.Message
			if len(p.Options) != 2 || p.Options[0].ID != OpenAICodexBrowserLoginMethod || p.Options[1].ID != OpenAICodexDeviceCodeLoginMethod {
				t.Fatalf("options wrong: %+v", p.Options)
			}
			return OpenAICodexDeviceCodeLoginMethod, nil
		},
		OnDeviceCode: func(OAuthDeviceCodeInfo) { deviceShown = true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if creds.Access != "acc" || !deviceShown || selectMsg == "" {
		t.Fatalf("dispatch failed: creds=%+v deviceShown=%v msg=%q", creds, deviceShown, selectMsg)
	}
}

func TestCodexProvider_Login_CancelledOnEmptySelect(t *testing.T) {
	_, err := CodexOAuthProvider{}.Login(OAuthLoginCallbacks{
		OnSelect: func(OAuthSelectPrompt) (string, error) { return "", nil },
	})
	if err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("err = %v, want Login cancelled", err)
	}
}

func TestCodexProvider_Login_UnknownMethodErrors(t *testing.T) {
	_, err := CodexOAuthProvider{}.Login(OAuthLoginCallbacks{
		OnSelect: func(OAuthSelectPrompt) (string, error) { return "bogus-method", nil },
	})
	if err == nil || !strings.Contains(err.Error(), "Unknown OpenAI Codex login method") {
		t.Fatalf("err = %v, want unknown-method", err)
	}
}
