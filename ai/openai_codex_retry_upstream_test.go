package ai

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
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
}
