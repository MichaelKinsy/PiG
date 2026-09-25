package ai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The device-flow refresh contract comes from auth/oauth/kimi-coding.ts.
// A fake transport checks the production request and retry boundary without sockets.
func TestKimiRefreshRejectsInvalidGrantWithoutRetry(t *testing.T) {
	t.Parallel()
	calls := 0
	provider := kimiOAuthProvider{oauthHost: "https://auth.invalid", client: &http.Client{Transport: metaRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.URL.Path != "/api/oauth/token" || request.Form.Get("grant_type") != "refresh_token" || request.Form.Get("refresh_token") != "stored" {
			t.Errorf("refresh request = %s %v", request.URL, request.Form)
		}
		return metaJSONResponse(http.StatusBadRequest, map[string]any{"error": "invalid_grant"}), nil
	})}, sleep: func(context.Context, time.Duration) error { t.Error("invalid_grant must not back off"); return nil }}
	_, err := provider.RefreshToken(OAuthCredentials{Refresh: "stored"})
	if err == nil || !strings.Contains(err.Error(), "unauthorized") || calls != 1 {
		t.Fatalf("refresh: calls=%d err=%v", calls, err)
	}
}

// Provider constructors in qwen-token-plan.ts and qwen-token-plan-cn.ts bind
// their catalogs to different endpoints but the same completion API.
func TestQwenTokenPlanCatalogRoutes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ provider, base string }{
		{"qwen-token-plan", "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"},
		{"qwen-token-plan-cn", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"},
	} {
		model := mustGeneratedModel(t, tc.provider, "qwen3.8-max")
		if model.BaseURL != tc.base || model.API != APIOpenAICompletions {
			t.Errorf("%s route = %s %s", tc.provider, model.BaseURL, model.API)
		}
	}
}

// pi-messages.test.ts requires an error when SSE ends before its terminal
// event. An in-memory SSE body exercises that production path without a server.
func TestPiMessagesInMemoryPrematureEnd(t *testing.T) {
	t.Parallel()
	provider := NewPiMessagesProvider(PiMessagesConfig{BaseURL: "https://gateway.invalid/v1", APIKey: "token", Model: "auto", ProviderID: "radius"}).(*piMessagesProvider)
	provider.client = &http.Client{Transport: metaRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/messages" || request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request = %s %v", request.URL, request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"start\"}\n\n"))}, nil
	})}
	_, message := collectPiMessages(t, provider, StreamOptions{})
	if message.StopReason != StopReasonError || !strings.Contains(message.ErrorMessage, "stream ended without a terminal event") {
		t.Fatalf("premature end = %+v", message)
	}
}
