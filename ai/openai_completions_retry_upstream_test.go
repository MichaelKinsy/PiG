package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func completionsRetryProvider(attempts *atomic.Int32, statuses []int, headers http.Header) *openAIProvider {
	p := NewOpenAIProvider(OpenAIConfig{Model: "test-model", ProviderID: "opencode-go", BaseURL: "https://opencode.ai/zen/go/v1", APIKey: "test"}).(*openAIProvider)
	p.client = &http.Client{Transport: &retryTransport{base: openAITestRoundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		index := int(attempts.Add(1)) - 1
		if index < len(statuses) {
			status := statuses[index]
			message := "rate limited"
			if status == 500 {
				message = "server error"
			}
			return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: headers.Clone(), Body: io.NopCloser(strings.NewReader(message))}, nil
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"id\":\"chatcmpl-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"id\":\"chatcmpl-test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))}, nil
	})}}
	return p
}

func consumeCompletionsRetry(ctx context.Context, p *openAIProvider) error {
	stream, err := p.Stream(ctx, NormalizeContext(Context{SystemPrompt: "", Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "hi"}}}}, Tools: []ToolSchema{}}), StreamOptions{})
	if err != nil {
		return err
	}
	for range stream.Events(ctx) {
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		return fmt.Errorf("result=%#v", result)
	}
	return nil
}

type retryCancellationBody struct {
	ctx     context.Context
	started chan struct{}
	closed  atomic.Bool
}

func (body *retryCancellationBody) Read([]byte) (int, error) {
	close(body.started)
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (body *retryCancellationBody) Close() error { body.closed.Store(true); return nil }

func TestRetryLimitBodyReadCancellationPreservesContextAndClosesBody(t *testing.T) {
	resetProviderRetry(t)
	if err := ConfigureProviderRetry(2, 1000); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	body := &retryCancellationBody{ctx: ctx, started: make(chan struct{})}
	transport := &retryTransport{base: openAITestRoundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Status: "429 Too Many Requests", Header: http.Header{"Retry-After": []string{"277403"}}, Body: body}, nil
	})}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() { _, err := transport.RoundTrip(request); completed <- err }()
	<-body.started
	cancel()
	if err := <-completed; !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "Server requested") {
		t.Fatalf("error=%v", err)
	}
	if !body.closed.Load() {
		t.Fatal("error response body was not closed")
	}
}

func TestOpenAICompletionsRetryUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openai-completions-retry.test.ts:88
	t.Run("disables SDK retries by default", func(t *testing.T) {
		resetProviderRetry(t)
		var attempts atomic.Int32
		if err := consumeCompletionsRetry(t.Context(), completionsRetryProvider(&attempts, nil, nil)); err != nil {
			t.Fatal(err)
		}
		if attempts.Load() != 1 {
			t.Fatalf("attempts=%d", attempts.Load())
		}
		attempts.Store(0)
		if err := consumeCompletionsRetry(t.Context(), completionsRetryProvider(&attempts, []int{429}, nil)); err == nil {
			t.Fatal("default retry policy hid a failed request")
		}
		if attempts.Load() != 1 {
			t.Fatalf("default policy retried: %d attempts", attempts.Load())
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-retry.test.ts:93
	t.Run("honors provider retries while keeping SDK retries disabled", func(t *testing.T) {
		resetProviderRetry(t)
		if err := ConfigureProviderRetry(2, 100); err != nil {
			t.Fatal(err)
		}
		synctest.Test(t, func(t *testing.T) {
			var attempts atomic.Int32
			provider := completionsRetryProvider(&attempts, []int{429, 500}, http.Header{"Retry-After-Ms": []string{"100"}})
			completed := make(chan error, 1)
			go func() { completed <- consumeCompletionsRetry(t.Context(), provider) }()
			synctest.Wait()
			if attempts.Load() != 1 {
				t.Fatalf("initial attempts=%d", attempts.Load())
			}
			time.Sleep(99 * time.Millisecond)
			synctest.Wait()
			if attempts.Load() != 1 {
				t.Fatalf("early first retry: %d", attempts.Load())
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			if attempts.Load() != 2 {
				t.Fatalf("first retry attempts=%d", attempts.Load())
			}
			time.Sleep(99 * time.Millisecond)
			synctest.Wait()
			if attempts.Load() != 2 {
				t.Fatalf("early second retry: %d", attempts.Load())
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			if err := <-completed; err != nil {
				t.Fatal(err)
			}
			if attempts.Load() != 3 {
				t.Fatalf("final attempts=%d", attempts.Load())
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-retry.test.ts:125
	t.Run("fails immediately when a provider-requested retry delay exceeds the limit", func(t *testing.T) {
		resetProviderRetry(t)
		if err := ConfigureProviderRetry(2, 1000); err != nil {
			t.Fatal(err)
		}
		var attempts atomic.Int32
		err := consumeCompletionsRetry(t.Context(), completionsRetryProvider(&attempts, []int{429}, http.Header{"Retry-After": []string{"277403"}}))
		if err == nil || !strings.Contains(err.Error(), "Server requested 277403s retry delay (max: 1s)") || !strings.Contains(err.Error(), "rate limited") {
			t.Fatalf("error=%v", err)
		}
		if attempts.Load() != 1 {
			t.Fatalf("attempts=%d", attempts.Load())
		}
	})
}
