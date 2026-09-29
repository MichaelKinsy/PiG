package ai

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPairReviewMistralCustomFetchKeepsOriginTarget(t *testing.T) {
	for _, custom := range []bool{false, true} {
		target := make(chan string, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target <- r.RequestURI
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(mistralUpstreamTerminal))
		}))
		provider := NewMistralProvider(MistralConfig{APIKey: "fixture", Model: "model", BaseURL: server.URL + "/a|b"})
		opts := StreamOptions{}
		if custom {
			opts.Fetch = server.Client()
		}
		stream, err := provider.Stream(t.Context(), mistralUpstreamContext(), opts)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		result := stream.Result()
		server.Close()
		if result.StopReason != StopReasonStop {
			t.Fatal(result)
		}
		if got := <-target; got != "/a|b/v1/chat/completions" {
			t.Errorf("custom=%v request target=%q, want origin form", custom, got)
		}
	}
}
