package ai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// google-generative-ai.ts and google-vertex.ts wrap generateContentStream in retryGoogleRequest (google-shared.ts:494-514), and @google/genai
// does not retry, so retryProviderRequest is the only retry layer. The SDK's ApiError has no headers, so a retry-after header is not
// honoured: the request is retried by status with the default backoff. Probed with Pi 1.1.0 (retry.provider.maxRetries 2, a server that
// always answers 429): 3 requests, and with retry-after 120 still 3 requests and the 429 error. PiG also retried in the HTTP transport, so
// it sent 9 requests, and with retry-after 120 it failed at once with "Server requested 120s retry delay".
func TestGoogleRetryGoogleRequestIsTheOnlyRetryLayer(t *testing.T) {
	beforeRetries, beforeDelay := ConfiguredProviderRetry()
	t.Cleanup(func() { _ = ConfigureProviderRetry(beforeRetries, beforeDelay) })
	if err := ConfigureProviderRetry(2, 60000); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, header, value string
	}{
		{"retry-after-ms", "Retry-After-Ms", "1"},
		{"retry-after above the cap", "Retry-After", "120"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.Header().Set(tc.header, tc.value)
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprint(w, `{"error":{"code":429,"message":"slow","status":"RESOURCE_EXHAUSTED"}}`)
			}))
			defer server.Close()
			provider := NewGoogleProvider(GoogleConfig{Model: "gemini-2.5-flash", APIKey: "test", BaseURL: server.URL})
			delay := 60000
			_, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{MaxRetries: new(2), MaxRetryDelayMs: &delay})
			if err == nil || strings.Contains(err.Error(), "retry delay") || !strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
				t.Fatalf("error=%v, want the 429 error", err)
			}
			if got := attempts.Load(); got != 3 {
				t.Fatalf("attempts=%d, want 3", got)
			}
		})
	}
}
