package ai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type anthropicEOFBarrierBody struct {
	reader              *strings.Reader
	ctx                 context.Context
	waiting, closed     chan struct{}
	waitOnce, closeOnce sync.Once
}

func (body *anthropicEOFBarrierBody) Read(p []byte) (int, error) {
	n, err := body.reader.Read(p)
	if err != io.EOF {
		return n, err
	}
	body.waitOnce.Do(func() { close(body.waiting) })
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (body *anthropicEOFBarrierBody) Close() error {
	body.closeOnce.Do(func() { close(body.closed) })
	return nil
}

// Pi keeps consuming after message_stop; cancellation still owns the response body and terminal result while EOF is pending.
func TestAnthropicSSECancellationAfterMessageStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	body := &anthropicEOFBarrierBody{reader: strings.NewReader(anthropicMinimalFixture()), waiting: make(chan struct{}), closed: make(chan struct{})}
	fetch := &http.Client{Transport: responsesTestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body.ctx = request.Context()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
	})}
	provider := NewAnthropicProvider(AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key", BaseURL: "http://127.0.0.1:9"})
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{Fetch: fetch})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-body.closed:
		t.Fatal("provider finalized and closed the response at message_stop before iterator EOF")
	case <-body.waiting:
	}
	cancel()
	result := stream.Result()
	if result.StopReason != StopReasonAborted || result.RawStopReason != "end_turn" || result.Usage.TotalTokens != 17 {
		t.Fatalf("canceled result=%+v", result)
	}
	<-body.closed
}

// packages/ai/src/api/anthropic-messages.ts:470-509,791-816 consumes the complete SSE iterator before producing its terminal result.
func TestAnthropicSSETerminalAfterIteratorEOF(t *testing.T) {
	minimal := anthropicMinimalFixture()
	for _, tc := range []struct {
		name, events string
		stop         StopReason
		errorMessage string
		total        int
	}{
		{"error after message_stop", minimal + anthropicFixtureEvent("error", "trailing error"), StopReasonError, "trailing error", 17},
		{"usage after message_stop", minimal + anthropicFixtureEvent("message_delta", `{"delta":{},"usage":{"input_tokens":23,"output_tokens":11}}`), StopReasonStop, "", 34},
		{"stop reason after message_stop", minimal + anthropicFixtureEvent("message_delta", `{"delta":{"stop_reason":"max_tokens"}}`), StopReasonLength, "", 17},
		{"delta without message_start", anthropicFixtureEvent("message_delta", `{"delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":2}}`), StopReasonStop, "", 3},
		{"missing message_stop still errors", strings.TrimSuffix(minimal, anthropicFixtureEvent("message_stop", `{}`)), StopReasonError, "Anthropic stream ended before message_stop", 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-haiku-4-5"}, tc.events, StreamOptions{})
			if result.StopReason != tc.stop || result.ErrorMessage != tc.errorMessage || result.Usage.TotalTokens != tc.total {
				t.Fatalf("stop=%s error=%q total=%d; want stop=%s error=%q total=%d", result.StopReason, result.ErrorMessage, result.Usage.TotalTokens, tc.stop, tc.errorMessage, tc.total)
			}
		})
	}
}

// packages/ai/src/api/anthropic-messages.ts:482-484 throws sse.data verbatim, without parsing it or adding a provider prefix.
func TestAnthropicSSEErrorDataIsVerbatim(t *testing.T) {
	for _, data := range []string{`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, "first\nsecond", "  preserve whitespace  "} {
		t.Run(data, func(t *testing.T) {
			events := "event: error\ndata: " + strings.ReplaceAll(data, "\n", "\ndata: ") + "\n\n"
			result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-haiku-4-5"}, events, StreamOptions{})
			if result.StopReason != StopReasonError || result.ErrorMessage != data {
				t.Fatalf("stop=%s error=%q; want error=%q", result.StopReason, result.ErrorMessage, data)
			}
		})
	}
}
