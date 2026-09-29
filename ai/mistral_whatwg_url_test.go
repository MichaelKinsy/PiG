package ai

import (
	"net/http"
	"testing"
)

// Pi requestMistralStream constructs WHATWG URLs; encoded dot segments and HTTP backslashes are normalized before resolving the request path.
func TestPairReviewMistralWHATWGBase(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://proxy.test/a/%2e%2e/b", "https://proxy.test/b/v1/chat/completions"},
		{"https://proxy.test/a\\b", "https://proxy.test/a/b/v1/chat/completions"},
		{"https://münich.example/root", "https://xn--mnich-kva.example/root/v1/chat/completions"},
	} {
		got, err := mistralChatCompletionsURL(tc.base)
		if err != nil || got != tc.want {
			t.Errorf("base=%q: got %q,%v want %q", tc.base, got, err, tc.want)
		}
		provider := NewMistralProvider(MistralConfig{APIKey: "fixture", Model: "model", BaseURL: tc.base})
		called := false
		stream, err := provider.Stream(t.Context(), mistralUpstreamContext(), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(request *http.Request) (*http.Response, error) {
			called = true
			if got := request.URL.String(); got != tc.want {
				t.Errorf("Provider base=%q: request=%q want=%q", tc.base, got, tc.want)
			}
			return mistralUpstreamSSE(mistralUpstreamTerminal), nil
		})}})
		if err != nil {
			t.Fatal(err)
		}
		if result := stream.Result(); !called || result.StopReason != StopReasonStop {
			t.Fatalf("called=%v result=%+v", called, result)
		}
	}
}
