package ai

import (
	"context"
	"net/http"
	"runtime"
	"testing"
	"testing/synctest"
)

// Ports the "removes SSE retry backoff abort listeners after the retry succeeds" case of packages/ai/test/openai-codex-stream.test.ts (Pi #11725, a
// regression test for #11708).
//
// Pi's sleep() attached an abort listener to the caller's signal for every backoff and never removed it, so three 429 responses followed by a
// success left three listeners on the signal. PiG's backoff waits on the request context and a timer that it stops when the wait ends, and
// registers nothing on the caller's context, so there is no listener count to read. The same inputs (three 429 responses with retry-after 0, then a
// completed stream, maxRetries 3, the caller's own cancelable context) check the equivalent: the retries leave no goroutine behind.
func TestCodexSSERetryBackoffReleasesTheCallerContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(*http.Request) (*http.Response, error) {
			calls++
			if calls <= 3 {
				response := codexJSONResp(429, `{"error":{"message":"rate limited"}}`)
				response.Header.Set("retry-after", "0")
				return response, nil
			}
			return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
		}))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		synctest.Wait()
		before := runtime.NumGoroutine()

		stream, err := provider.Stream(WithProviderMaxRetries(ctx, 3), codexUpstreamContext(), StreamOptions{Transport: TransportSSE})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		synctest.Wait()

		if result.StopReason != StopReasonStop {
			t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
		}
		if calls != 4 {
			t.Fatalf("calls = %d, want 4", calls)
		}
		if after := runtime.NumGoroutine(); after > before {
			t.Fatalf("goroutines after the retries = %d, before = %d", after, before)
		}
	})
}
