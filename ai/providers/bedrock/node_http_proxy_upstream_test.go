//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// .upstream/v0.87.1/packages/ai/test/node-http-proxy.test.ts:57 — prefers scoped proxy env aliases before process env aliases.
// Drive the production Bedrock request rather than only a resolver helper.
func TestBedrockScopedProxyRequest(t *testing.T) {
	clearNodeProxyEnv(t)
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Host != "bedrock.example.invalid" {
			t.Errorf("proxy request target = %q", r.URL.Host)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"fixture rejection"}`)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	provider := NewBedrockProvider("anthropic.claude-test", "http://bedrock.example.invalid")
	stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), ai.StreamOptions{Env: ai.ProviderEnv{
		"HTTP_PROXY": proxy.URL, "AWS_REGION": "us-east-1", "AWS_BEDROCK_SKIP_AUTH": "1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != ai.StopReasonError || !strings.Contains(result.ErrorMessage, "fixture rejection") {
		t.Fatalf("want fixture HTTP rejection event, got %+v", result)
	}
	if requests.Load() != 1 {
		t.Fatalf("scoped proxy received %d requests, want 1; error: %v", requests.Load(), err)
	}
}

func clearNodeProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "no_proxy", "all_proxy", "npm_config_http_proxy", "npm_config_https_proxy", "npm_config_proxy", "npm_config_no_proxy"} {
		t.Setenv(key, "")
	}
}
