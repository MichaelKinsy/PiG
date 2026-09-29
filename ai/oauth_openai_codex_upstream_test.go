package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type codexOAuthReply struct {
	status int
	body   string
}
type codexOAuthProbe struct {
	mu     sync.Mutex
	times  []int64
	access string
}

func (probe *codexOAuthProbe) pollTimes() []int64 {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return slices.Clone(probe.times)
}

func mockCodexOAuthUpstream(t *testing.T, account, userCode, interval string, replies []codexOAuthReply) *codexOAuthProbe {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	payload, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account}})
	probe := &codexOAuthProbe{access: base64.StdEncoding.EncodeToString(header) + "." + base64.StdEncoding.EncodeToString(payload) + ".signature"}
	withMockCodexClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" {
			return nil, fmt.Errorf("method=%s", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		switch r.URL.String() {
		case "https://auth.openai.com/api/accounts/deviceauth/usercode":
			var got map[string]any
			if err = json.Unmarshal(body, &got); err != nil {
				return nil, err
			}
			if r.Header.Get("Content-Type") != "application/json" || !reflect.DeepEqual(got, map[string]any{"client_id": "app_EMoamEEZ73f0CkXaXp7hrann"}) {
				return nil, fmt.Errorf("usercode request: headers=%v body=%s", r.Header, body)
			}
			return codexJSONResp(200, fmt.Sprintf(`{"device_auth_id":"device-auth-id","user_code":%q,"interval":%q}`, userCode, interval)), nil
		case "https://auth.openai.com/api/accounts/deviceauth/token":
			var got map[string]any
			if err = json.Unmarshal(body, &got); err != nil {
				return nil, err
			}
			if r.Header.Get("Content-Type") != "application/json" || !reflect.DeepEqual(got, map[string]any{"device_auth_id": "device-auth-id", "user_code": userCode}) {
				return nil, fmt.Errorf("poll request: headers=%v body=%s", r.Header, body)
			}
			probe.mu.Lock()
			probe.times = append(probe.times, time.Now().UnixMilli())
			index := len(probe.times) - 1
			probe.mu.Unlock()
			if len(replies) == 0 {
				return codexJSONResp(403, `{"error":{"message":"Device authorization is pending. Please try again.","type":"invalid_request_error","param":null,"code":"deviceauth_authorization_pending"}}`), nil
			}
			if index >= len(replies) {
				return nil, errors.New("unexpected extra device auth poll")
			}
			return codexJSONResp(replies[index].status, replies[index].body), nil
		case "https://auth.openai.com/oauth/token":
			values, err := url.ParseQuery(string(body))
			if err != nil {
				return nil, err
			}
			want := url.Values{"grant_type": {"authorization_code"}, "client_id": {"app_EMoamEEZ73f0CkXaXp7hrann"}, "code": {"oauth-code"}, "redirect_uri": {"https://auth.openai.com/deviceauth/callback"}, "code_verifier": {"device-code-verifier"}}
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || !reflect.DeepEqual(values, want) {
				return nil, fmt.Errorf("exchange request: headers=%v form=%v", r.Header, values)
			}
			return codexJSONResp(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":"refresh-token","expires_in":3600}`, probe.access)), nil
		}
		return nil, fmt.Errorf("unexpected URL: %s", r.URL)
	})
	return probe
}

func codexApprovedReply() codexOAuthReply {
	return codexOAuthReply{200, `{"authorization_code":"oauth-code","code_challenge":"device-code-challenge","code_verifier":"device-code-verifier"}`}
}
func codexLoginForTest(ctx context.Context, notify func(OAuthDeviceCodeInfo)) (OAuthCredentials, error) {
	return (CodexOAuthProvider{}).LoginContext(ctx, OAuthLoginCallbacks{OnSelect: func(OAuthSelectPrompt) (string, error) { return "device_code", nil }, OnDeviceCode: notify})
}
func assertCodexAccountCredentials(t *testing.T, credentials OAuthCredentials, access, account string) {
	t.Helper()
	data, err := json.Marshal(credentials)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if credentials.Access != access || credentials.Refresh != "refresh-token" || decoded["accountId"] != account {
		t.Fatalf("credentials=%s, want accountId=%s", data, account)
	}
}

func TestOpenAICodexOAuthUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openai-codex-oauth.test.ts:77
	t.Run("logs in with the OpenAI Codex device code flow", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			start := time.Date(2026, time.May, 20, 0, 0, 0, 0, time.UTC)
			time.Sleep(start.Sub(time.Now()))
			probe := mockCodexOAuthUpstream(t, "account-123", "ABCD-1234", "5", []codexOAuthReply{{403, `{"error":{"message":"Device authorization is pending. Please try again.","type":"invalid_request_error","param":null,"code":"deviceauth_authorization_pending"}}`}, codexApprovedReply()})
			var infos []OAuthDeviceCodeInfo
			type outcome struct {
				credentials OAuthCredentials
				err         error
			}
			completed := make(chan outcome, 1)
			go func() {
				credentials, err := codexLoginForTest(t.Context(), func(info OAuthDeviceCodeInfo) { infos = append(infos, info) })
				completed <- outcome{credentials, err}
			}()
			synctest.Wait()
			if !reflect.DeepEqual(infos, []OAuthDeviceCodeInfo{{UserCode: "ABCD-1234", VerificationURI: "https://auth.openai.com/codex/device", IntervalSeconds: 5, ExpiresInSeconds: 900}}) {
				t.Fatalf("device infos=%#v", infos)
			}
			if !reflect.DeepEqual(probe.pollTimes(), []int64{start.UnixMilli()}) {
				t.Fatalf("poll times=%v", probe.pollTimes())
			}
			time.Sleep(4999 * time.Millisecond)
			synctest.Wait()
			if !reflect.DeepEqual(probe.pollTimes(), []int64{start.UnixMilli()}) {
				t.Fatalf("early poll=%v", probe.pollTimes())
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			result := <-completed
			if result.err != nil {
				t.Fatal(result.err)
			}
			assertCodexAccountCredentials(t, result.credentials, probe.access, "account-123")
			if result.credentials.Expires != start.UnixMilli()+5000+3600*1000 {
				t.Fatalf("expires=%d", result.credentials.Expires)
			}
			if !reflect.DeepEqual(probe.pollTimes(), []int64{start.UnixMilli(), start.UnixMilli() + 5000}) {
				t.Fatalf("poll times=%v", probe.pollTimes())
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-oauth.test.ts:180
	t.Run("offers browser login first and uses the selected OpenAI Codex device code flow", func(t *testing.T) {
		probe := mockCodexOAuthUpstream(t, "account-456", "WXYZ-7890", "5", []codexOAuthReply{codexApprovedReply()})
		var prompts []OAuthSelectPrompt
		var infos []OAuthDeviceCodeInfo
		credentials, err := (CodexOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{OnSelect: func(prompt OAuthSelectPrompt) (string, error) {
			prompts = append(prompts, prompt)
			return "device_code", nil
		}, OnPrompt: func(OAuthPrompt) (string, error) { return "", errors.New("text prompt should not be used") }, OnAuth: func(OAuthAuthInfo) { t.Error("browser login should not start") }, OnDeviceCode: func(info OAuthDeviceCodeInfo) { infos = append(infos, info) }})
		if err != nil {
			t.Fatal(err)
		}
		assertCodexAccountCredentials(t, credentials, probe.access, "account-456")
		if !reflect.DeepEqual(prompts, []OAuthSelectPrompt{{Message: "Select OpenAI Codex login method:", Options: []OAuthSelectOption{{ID: "browser", Label: "Browser login (default)"}, {ID: "device_code", Label: "Device code login (headless)"}}}}) {
			t.Fatalf("prompts=%#v", prompts)
		}
		if !reflect.DeepEqual(infos, []OAuthDeviceCodeInfo{{UserCode: "WXYZ-7890", VerificationURI: "https://auth.openai.com/codex/device", IntervalSeconds: 5, ExpiresInSeconds: 900}}) {
			t.Fatalf("infos=%#v", infos)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-oauth.test.ts:266
	t.Run("cancels when OpenAI Codex login method selection is cancelled", func(t *testing.T) {
		_, err := (CodexOAuthProvider{}).LoginContext(t.Context(), OAuthLoginCallbacks{OnSelect: func(OAuthSelectPrompt) (string, error) { return "", errors.New("Login cancelled") }})
		if err == nil || err.Error() != "Login cancelled" {
			t.Fatalf("error=%v", err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-oauth.test.ts:278
	t.Run("cancels the OpenAI Codex device code flow while waiting", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			probe := mockCodexOAuthUpstream(t, "unused", "ABCD-1234", "5", nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			completed := make(chan error, 1)
			go func() { _, err := codexLoginForTest(ctx, func(OAuthDeviceCodeInfo) {}); completed <- err }()
			synctest.Wait()
			if len(probe.pollTimes()) != 1 {
				t.Fatalf("poll times=%v", probe.pollTimes())
			}
			cancel()
			synctest.Wait()
			if err := <-completed; err == nil || err.Error() != "Login cancelled" {
				t.Fatalf("error=%v", err)
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-oauth.test.ts:323
	t.Run("times out the OpenAI Codex device code flow after 15 minutes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			probe := mockCodexOAuthUpstream(t, "unused", "ABCD-1234", "60", nil)
			completed := make(chan error, 1)
			go func() { _, err := codexLoginForTest(t.Context(), func(OAuthDeviceCodeInfo) {}); completed <- err }()
			synctest.Wait()
			if len(probe.pollTimes()) != 1 {
				t.Fatalf("poll times=%v", probe.pollTimes())
			}
			time.Sleep(15 * time.Minute)
			synctest.Wait()
			if err := <-completed; err == nil || err.Error() != "Device flow timed out" {
				t.Fatalf("error=%v", err)
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-oauth.test.ts:366
	t.Run("treats OpenAI Codex device auth 403 and 404 responses as pending", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			probe := mockCodexOAuthUpstream(t, "account-403-404", "ABCD-1234", "1", []codexOAuthReply{{403, `{"error":"access_denied","error_description":"denied"}`}, {404, "not ready"}, codexApprovedReply()})
			type outcome struct {
				credentials OAuthCredentials
				err         error
			}
			completed := make(chan outcome, 1)
			go func() {
				credentials, err := codexLoginForTest(t.Context(), func(OAuthDeviceCodeInfo) {})
				completed <- outcome{credentials, err}
			}()
			synctest.Wait()
			time.Sleep(time.Second)
			synctest.Wait()
			time.Sleep(time.Second)
			synctest.Wait()
			result := <-completed
			if result.err != nil {
				t.Fatal(result.err)
			}
			assertCodexAccountCredentials(t, result.credentials, probe.access, "account-403-404")
			if len(probe.pollTimes()) != 3 {
				t.Fatalf("poll times=%v", probe.pollTimes())
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-oauth.test.ts:428
	t.Run("includes the response body in OpenAI Codex device auth poll failures", func(t *testing.T) {
		mockCodexOAuthUpstream(t, "unused", "ABCD-1234", "5", []codexOAuthReply{{500, `{"error":"server_error","error_description":"try again later"}`}})
		_, err := codexLoginForTest(t.Context(), func(OAuthDeviceCodeInfo) {})
		if err == nil || err.Error() != `OpenAI Codex device auth failed with status 500: {"error":"server_error","error_description":"try again later"}` {
			t.Fatalf("error=%v", err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-oauth.test.ts:456
	t.Run("does not write token refresh failures to stderr", func(t *testing.T) {
		withMockCodexClient(t, func(*http.Request) (*http.Response, error) {
			return codexJSONResp(401, `{"error":{"message":"Could not validate your token. Please try signing in again.","type":"invalid_request_error"}}`), nil
		})
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		old := os.Stderr
		os.Stderr = writer
		t.Cleanup(func() { os.Stderr = old; _ = writer.Close(); _ = reader.Close() })
		_, refreshErr := (CodexOAuthProvider{}).RefreshTokenContext(t.Context(), OAuthCredentials{Access: "invalid-access-token", Refresh: "invalid-refresh-token", Expires: 0})
		_ = writer.Close()
		os.Stderr = old
		output, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if refreshErr == nil || !strings.Contains(refreshErr.Error(), "OpenAI Codex token refresh failed (401)") || !strings.Contains(refreshErr.Error(), "Could not validate your token") {
			t.Fatalf("error=%v", refreshErr)
		}
		if len(output) != 0 {
			t.Fatalf("stderr=%q", output)
		}
	})
}
