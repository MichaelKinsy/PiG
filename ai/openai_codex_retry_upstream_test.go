package ai

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

func TestCodexSSERetriesUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:2405
	for _, tc := range []struct {
		name, header, value string
		delay               time.Duration
	}{{"retry-after-ms", "retry-after-ms", "1500", 1500 * time.Millisecond}, {"retry-after seconds", "retry-after", "60", 60 * time.Second}, {"retry-after HTTP date", "retry-after", "", 45 * time.Second}} {
		t.Run("uses "+tc.name+" for SSE retries", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				time.Sleep(time.Date(2026, time.May, 13, 0, 0, 0, 0, time.UTC).Sub(time.Now()))
				if tc.value == "" {
					tc.value = time.Now().Add(tc.delay).UTC().Format(http.TimeFormat)
				}
				var times []time.Time
				provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(*http.Request) (*http.Response, error) {
					times = append(times, time.Now())
					if len(times) == 1 {
						response := codexJSONResp(429, `{"error":{"code":"rate_limit_exceeded","message":"rate limited"}}`)
						response.Header.Set(tc.header, tc.value)
						return response, nil
					}
					return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
				}))
				start := time.Now()
				stream, err := provider.Stream(WithProviderMaxRetries(t.Context(), 1), codexUpstreamContext(), StreamOptions{Transport: TransportSSE})
				if err != nil {
					t.Fatal(err)
				}
				result := stream.Result()
				if codexUpstreamText(result) != "Hello" || !reflect.DeepEqual(times, []time.Time{start, start.Add(tc.delay)}) {
					t.Fatalf("result=%#v times=%v", result, times)
				}
			})
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:2479
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprintf("fails immediately when a %d retry delay exceeds the limit", status), func(t *testing.T) {
			oldRetries, oldLimit := ConfiguredProviderRetry()
			if err := ConfigureProviderRetry(3, 1000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ConfigureProviderRetry(oldRetries, oldLimit) })
			calls := 0
			provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				response := codexJSONResp(status, `{"error":{"code":"temporarily_unavailable","message":"retry later"}}`)
				response.Header.Set("retry-after", "2")
				return response, nil
			}))
			stream, err := provider.Stream(context.Background(), codexUpstreamContext(), StreamOptions{Transport: TransportSSE})
			err = awaitProviderFailure(t, stream, err)
			if calls != 1 || err == nil || err.Error() != "Server requested 2s retry delay (max: 1s)" {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:2592
	t.Run("uses exponential backoff across repeated SSE retries without retry headers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			time.Sleep(time.Date(2026, time.May, 13, 0, 0, 0, 0, time.UTC).Sub(time.Now()))
			var times []time.Time
			provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(*http.Request) (*http.Response, error) {
				times = append(times, time.Now())
				if len(times) <= 3 {
					return codexJSONResp(429, `{"error":{"code":"rate_limit_exceeded","message":"rate limited"}}`), nil
				}
				return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
			}))
			start := time.Now()
			stream, err := provider.Stream(WithProviderMaxRetries(t.Context(), 3), codexUpstreamContext(), StreamOptions{Transport: TransportSSE})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if codexUpstreamText(result) != "Hello" || !reflect.DeepEqual(times, []time.Time{start, start.Add(time.Second), start.Add(3 * time.Second), start.Add(7 * time.Second)}) {
				t.Fatalf("result=%#v times=%v", result, times)
			}
		})
	})
	// .upstream/current/packages/ai/test/openai-codex-stream.test.ts:324 (Pi #11708): three 429 answers with retry-after 0 and a
	// fourth that succeeds. Pi asserts getEventListeners(signal, "abort") is empty after the result, i.e. a finished backoff
	// leaves nothing registered on the caller's signal. The Go carrier of an abort listener is a goroutine (or AfterFunc) parked
	// on the request context, so the assertion is that the goroutine count seen by the fourth request equals the first
	// request's (no backoff left a waiter) and that nothing remains once the result is read.
	t.Run("removes SSE retry backoff abort waiters after the retry succeeds", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			calls := 0
			var goroutinesAtCall []int
			provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				goroutinesAtCall = append(goroutinesAtCall, runtime.NumGoroutine())
				if calls <= 3 {
					response := codexJSONResp(429, `{"error":{"message":"rate limited"}}`)
					response.Header.Set("retry-after", "0")
					return response, nil
				}
				return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
			}))
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			synctest.Wait()
			before := runtime.NumGoroutine()
			stream, err := provider.Stream(WithProviderMaxRetries(ctx, 3), codexUpstreamContext(), StreamOptions{Transport: TransportSSE})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			synctest.Wait()
			if result.StopReason != StopReasonStop || calls != 4 {
				t.Fatalf("stopReason=%v calls=%d", result.StopReason, calls)
			}
			if goroutinesAtCall[3] != goroutinesAtCall[0] {
				t.Fatalf("goroutines at each request = %v; a finished retry backoff still waits on the request context", goroutinesAtCall)
			}
			if after := runtime.NumGoroutine(); after > before {
				t.Fatalf("goroutines %d -> %d after the retried request completed", before, after)
			}
		})
	})
}
