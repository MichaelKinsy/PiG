//go:build !pig_strip_mistral_conversations

package ai

import (
	"net/http"
	"strings"
	"testing"
)

func TestMistralRequestURLUsesURLResolution(t *testing.T) {
	// Pi requestMistralStream normalizes the base pathname's trailing slash, then resolves v1/chat/completions as a relative URL.
	for _, tc := range []struct{ base, want string }{
		{"", "https://api.mistral.ai/v1/chat/completions"},
		{"https://api.mistral.ai", "https://api.mistral.ai/v1/chat/completions"},
		{"https://proxy.test/root?token=abc#frag", "https://proxy.test/root/v1/chat/completions"},
		{"https://proxy.test/root/v1", "https://proxy.test/root/v1/v1/chat/completions"},
		{"https://proxy.test/root///", "https://proxy.test/root/v1/chat/completions"},
		{"https://proxy.test/a%2Fb", "https://proxy.test/a%2Fb/v1/chat/completions"},
		{"https://PROXY.TEST:443/root", "https://proxy.test/root/v1/chat/completions"},
		{"https://proxy.test/a/../b", "https://proxy.test/b/v1/chat/completions"},
		{"https://%65xample.test/base", "https://example.test/base/v1/chat/completions"},
		{"https://example.test:00443/base", "https://example.test/base/v1/chat/completions"},
		{"https:/example.test/base", "https://example.test/base/v1/chat/completions"},
		{"https:example.test/base", "https://example.test/base/v1/chat/completions"},
		{"https:////example.test/base", "https://example.test/base/v1/chat/completions"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			provider := NewMistralProvider(MistralConfig{APIKey: "fixture", Model: "model", BaseURL: tc.base})
			called := false
			stream, err := provider.Stream(t.Context(), mistralUpstreamContext(), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(request *http.Request) (*http.Response, error) {
				called = true
				if got := request.URL.String(); got != tc.want {
					t.Errorf("request URL=%q want=%q", got, tc.want)
				}
				return mistralUpstreamSSE(mistralUpstreamTerminal), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); !called || result.StopReason != StopReasonStop {
				t.Fatalf("called=%t result=%+v", called, result)
			}
		})
	}
}

func TestMistralRejectsInvalidBaseBeforeFetch(t *testing.T) {
	for _, base := range []string{"not-a-url", "mailto:address", "https://", "https://example.test:65536/base"} {
		t.Run(base, func(t *testing.T) {
			provider := NewMistralProvider(MistralConfig{APIKey: "fixture", Model: "model", BaseURL: base})
			called := false
			_, err := provider.Stream(t.Context(), mistralUpstreamContext(), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
				called = true
				return mistralUpstreamSSE(mistralUpstreamTerminal), nil
			})}})
			if called || err == nil || !strings.Contains(err.Error(), "Invalid URL") {
				t.Fatalf("called=%t error=%v", called, err)
			}
		})
	}
}

func BenchmarkMistralRequestURL(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		endpoint, err := mistralChatCompletionsURL("https://PROXY.TEST:443/root/a%2Fb?old=query#old")
		if err != nil || endpoint != "https://proxy.test/root/a%2Fb/v1/chat/completions" {
			b.Fatalf("endpoint=%q error=%v", endpoint, err)
		}
	}
}
