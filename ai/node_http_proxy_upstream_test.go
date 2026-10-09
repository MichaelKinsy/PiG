//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNodeHTTPProxyUpstream(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		env                     ProviderEnv
		process                 ProviderEnv
		target, want, errorText string
	}{
		// .upstream/v0.87.1/packages/ai/test/node-http-proxy.test.ts:40
		{name: "respects NO_PROXY exclusions", process: ProviderEnv{"HTTPS_PROXY": "http://proxy.example:8080", "NO_PROXY": "bedrock-runtime.us-east-1.amazonaws.com"}, target: "https://bedrock-runtime.us-east-1.amazonaws.com"},
		// .upstream/v0.87.1/packages/ai/test/node-http-proxy.test.ts:48
		{name: "resolves HTTP and HTTPS proxy URLs", process: ProviderEnv{"HTTPS_PROXY": "http://proxy.example:8080"}, target: "https://bedrock-runtime.us-east-1.amazonaws.com", want: "http://proxy.example:8080/"},
		// .upstream/v0.87.1/packages/ai/test/node-http-proxy.test.ts:57
		{name: "prefers scoped proxy env aliases before process env aliases", process: ProviderEnv{"https_proxy": "http://process-proxy.example:8080"}, env: ProviderEnv{"HTTPS_PROXY": "http://scoped-proxy.example:8080"}, target: "https://bedrock-runtime.us-east-1.amazonaws.com", want: "http://scoped-proxy.example:8080/"},
		// .upstream/v0.87.1/packages/ai/test/node-http-proxy.test.ts:68
		{name: "rejects SOCKS and PAC proxy URLs explicitly", process: ProviderEnv{"HTTPS_PROXY": "socks5://proxy.example:1080"}, target: "https://bedrock-runtime.us-east-1.amazonaws.com", errorText: UnsupportedProxyProtocolMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearNodeProxyEnv(t)
			for key, value := range tc.process {
				t.Setenv(key, value)
			}
			got, err := ResolveHTTPProxyURLForTarget(tc.target, tc.env)
			if tc.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorText) {
					t.Fatalf("error = %v, want %q", err, tc.errorText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			text := ""
			if got != nil {
				text = got.String()
			}
			if text != tc.want {
				t.Fatalf("proxy = %q, want %q", text, tc.want)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/node-http-proxy.test.ts:77 — every hostname/port assertion.
	t.Run("handles subdomain wildcards, IPv6, and ports in NO_PROXY", func(t *testing.T) {
		clearNodeProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "http://proxy.example:8080")
		t.Setenv("NO_PROXY", "example.com, .wildcard.org, *.star.net, ::1, [2001:db8::1], 127.0.0.1:8080")
		for _, host := range []string{"example.com", "api.example.com", "wildcard.org", "api.wildcard.org", "star.net", "api.star.net", "notexample.com", "[::1]:80", "[2001:db8::1]", "127.0.0.1:8080", "127.0.0.1:3000"} {
			got, err := ResolveHTTPProxyURLForTarget("https://"+host, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if host == "notexample.com" || host == "127.0.0.1:3000" {
				want = "http://proxy.example:8080/"
			}
			text := ""
			if got != nil {
				text = got.String()
			}
			if text != want {
				t.Errorf("%s proxy = %q, want %q", host, text, want)
			}
		}
	})
}

func clearNodeProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "no_proxy", "all_proxy", "npm_config_http_proxy", "npm_config_https_proxy", "npm_config_proxy", "npm_config_no_proxy"} {
		t.Setenv(key, "")
	}
}

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
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{Env: ProviderEnv{
		"HTTP_PROXY": proxy.URL, "AWS_REGION": "us-east-1", "AWS_BEDROCK_SKIP_AUTH": "1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, "fixture rejection") {
		t.Fatalf("want fixture HTTP rejection event, got %+v", result)
	}
	if requests.Load() != 1 {
		t.Fatalf("scoped proxy received %d requests, want 1; error: %v", requests.Load(), err)
	}
}
