package ai

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// packages/ai/src/api/openrouter-images.ts passes options.maxRetries and options.maxRetryDelayMs to retryProviderRequest, so
// a per-request retry budget retries a retryable failure and a server-requested delay above maxRetryDelayMs fails the request.
func TestOpenRouterImagesHonorPerRequestRetryOptions(t *testing.T) {
	model := ImageModel{ID: "m", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: "https://openrouter.ai/api/v1", Input: []string{"text"}, Output: []string{"image"}}
	request := ImagesContext{Input: []ContentBlock{TextContent{Text: "draw"}}}
	flaky := func(calls *atomic.Int32, retryAfterMs string) *http.Client {
		return &http.Client{Transport: fetchOptionTransport(func(r *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Retry-After-Ms": {retryAfterMs}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"busy"}}`)), Request: r}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(openRouterImagesUpstreamResponse)), Request: r}, nil
		})}
	}
	t.Run("maxRetries retries a retryable response", func(t *testing.T) {
		var calls atomic.Int32
		result, err := GenerateImages(t.Context(), model, request, ProviderImagesOptions{APIKey: "k", Fetch: flaky(&calls, "1"), MaxRetries: new(1)})
		if err != nil || result.StopReason != ImagesStopReasonStop || calls.Load() != 2 {
			t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
		}
	})
	t.Run("an explicit zero budget does not retry", func(t *testing.T) {
		var calls atomic.Int32
		result, _ := GenerateImages(t.Context(), model, request, ProviderImagesOptions{APIKey: "k", Fetch: flaky(&calls, "1"), MaxRetries: new(0)})
		if result.StopReason != ImagesStopReasonError || calls.Load() != 1 {
			t.Fatalf("result=%+v calls=%d", result, calls.Load())
		}
	})
	t.Run("maxRetryDelayMs rejects a longer server-requested delay", func(t *testing.T) {
		var calls atomic.Int32
		result, _ := GenerateImages(t.Context(), model, request, ProviderImagesOptions{APIKey: "k", Fetch: flaky(&calls, "60000"), MaxRetries: new(1), MaxRetryDelayMs: new(1000)})
		if result.StopReason != ImagesStopReasonError || !strings.Contains(result.ErrorMessage, "Server requested 60s retry delay (max: 1s)") || calls.Load() != 1 {
			t.Fatalf("result=%+v calls=%d", result, calls.Load())
		}
	})
}
