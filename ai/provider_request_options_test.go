package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestProviderRequestTimeoutEndsAtHeaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, ms := range []*int{nil, new(0), new(1234)} {
			ctx := withProviderRequestOptions(t.Context(), StreamOptions{TimeoutMs: ms})
			var bodyContext context.Context
			transport := &providerRequestTransport{base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				bodyContext = r.Context()
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("body")), Header: http.Header{}}, nil
			})}
			request, _ := http.NewRequestWithContext(ctx, "GET", "https://example.invalid", nil)
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(2 * time.Second)
			synctest.Wait()
			if bodyContext.Err() != nil {
				t.Fatal("header timeout canceled body after headers")
			}
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
func TestProviderRequestTimeoutCancelsHeaderAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := withProviderRequestOptions(t.Context(), StreamOptions{TimeoutMs: new(1234)})
		transport := &providerRequestTransport{base: roundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
		request, _ := http.NewRequestWithContext(ctx, "GET", "https://example.invalid", nil)
		start := time.Now()
		response, err := transport.RoundTrip(request)
		if response != nil {
			_ = response.Body.Close()
		}
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 1234*time.Millisecond {
			t.Fatalf("error=%v elapsed=%v", err, time.Since(start))
		}
	})
}

// Native provider dispatch must carry the options, not just expose plausible callback values.
func TestNativeProvidersUseRequestRetryDelayLimit(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages} {
		t.Run(string(api), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("retry-after-ms", "5000")
				w.WriteHeader(429)
				_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			}))
			defer server.Close()
			model := &GeneratedModel{ID: "fixture", Provider: "fixture", API: api}
			provider := newMatrixProvider(t, model, server.URL, false)
			defer func() { _ = provider.Close() }()
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{MaxRetries: new(2), MaxRetryDelayMs: new(3000)})
			if api == APIAnthropicMessages {
				result := requireAnthropicSetupError(t, stream, err)
				err = errors.New(result.ErrorMessage)
			} else {
				err = awaitProviderFailure(t, stream, err)
			}
			if !strings.Contains(err.Error(), "Server requested 5s retry delay (max: 3s)") {
				t.Fatalf("error=%v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("requests=%d", calls.Load())
			}
		})
	}
}
