package ai

// pi: packages/coding-agent/src/core/http-dispatcher.ts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Pi forwards timeoutMs:0 into the OpenAI/Anthropic SDK timer; Node schedules that timer after one millisecond rather than disabling it.
func TestProviderRequestExplicitZeroTimeoutRemainsArmed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		ctx := withProviderRequestOptions(parent, StreamOptions{TimeoutMs: new(0)})
		transport := &providerRequestTransport{base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid", nil)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		response, err := transport.RoundTrip(request)
		if response != nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != time.Millisecond || parent.Err() != nil {
			t.Fatalf("zero timeout error=%v elapsed=%s parent=%v", err, time.Since(start), parent.Err())
		}
	})
}

// Drive each production HTTP adapter, not just the timeout transport. The parent remains live when the explicit-zero per-attempt timer cancels the request.
func TestNativeProvidersKeepExplicitZeroTimeout(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages} {
		t.Run(string(api), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				if _, err := io.Copy(io.Discard, request.Body); err != nil {
					return
				}
				<-request.Context().Done()
			}))
			defer server.Close()
			provider := newMatrixProvider(t, &GeneratedModel{ID: "fixture", Provider: "fixture", API: api}, server.URL, false)
			defer func() { _ = provider.Close() }()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{TimeoutMs: new(0), MaxRetries: new(0)})
			switch {
			case api == APIAnthropicMessages:
				result := requireAnthropicSetupError(t, stream, err)
				if result.StopReason != StopReasonError || result.ErrorMessage != "Request timed out." {
					t.Fatalf("zero timeout did not fail request: %+v", result)
				}
			case err == nil:
				result := stream.Result()
				want := context.DeadlineExceeded.Error()
				if api == APIOpenAICompletions || api == APIOpenAIResponses {
					// OpenAI SDK APIConnectionTimeoutError carries its own message, not the transport's Go deadline cause.
					want = "Request timed out."
				}
				if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, want) {
					t.Fatalf("zero timeout did not fail request: %+v", result)
				}
			case !errors.Is(err, context.DeadlineExceeded):
				t.Fatalf("unrelated request failure: %v", err)
			}
			if ctx.Err() != nil {
				t.Fatalf("zero timeout was disabled; parent ended the request: %v", ctx.Err())
			}
		})
	}
}
