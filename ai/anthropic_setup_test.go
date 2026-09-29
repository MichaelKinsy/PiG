package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Pi anthropic-messages.ts:521-538 initializes the output before setup; :818-828 emits only error when setup rejects, retaining that output.
func requireAnthropicSetupError(t *testing.T, stream *AssistantMessageEventStream, err error) *AssistantMessage {
	t.Helper()
	if err != nil || stream == nil {
		t.Fatalf("setup escaped stream: stream=%v error=%v", stream != nil, err)
	}
	result := stream.Result()
	var events []AssistantMessageEvent
	for event := range stream.Events(t.Context()) {
		events = append(events, event)
	}
	if len(events) != 1 {
		t.Fatalf("setup events=%#v, want sole error", events)
	}
	failure, ok := events[0].(ErrorEvent)
	if !ok || failure.Error != result || failure.Reason != result.StopReason {
		t.Fatalf("event=%#v, result=%#v", events[0], result)
	}
	if result.StopReason != StopReasonError && result.StopReason != StopReasonAborted {
		t.Fatalf("stopReason=%q", result.StopReason)
	}
	if result.Content == nil || len(result.Content) != 0 || result.Usage != (Usage{}) || result.Timestamp == 0 || result.ErrorMessage == "" {
		t.Fatalf("uninitialized failure=%#v", result)
	}
	return result
}

func TestAnthropicSetupFailuresRetainResult(t *testing.T) {
	for _, shape := range []struct {
		name, provider, key string
		managed             bool
	}{
		{"api-key", "anthropic", "test-key", true},
		{"oauth", "anthropic", "sk-ant-oat-test", true},
		{"custom-no-catalog-model", "custom-proxy", "test-key", true},
		{"unmanaged", "custom-proxy", "test-key", false},
	} {
		for _, phase := range []string{"endpoint", "payload", "response", "http", "retry-payload", "retry-http"} {
			t.Run(shape.name+"/"+phase, func(t *testing.T) {
				cfg := AnthropicConfig{Model: "custom-model", ProviderID: shape.provider, APIKey: shape.key, BaseURL: "https://example.invalid", Compat: &ModelCompat{SupportsMidConvoEffort: new(shape.managed)}}
				sentinel := errors.New("setup rejected")
				if phase == "endpoint" {
					cfg.GetBaseURL = func(context.Context) (string, error) { return "", sentinel }
				}
				provider := NewAnthropicProvider(cfg)
				defer func() { _ = provider.Close() }()
				var order []string
				var bodies []*unreadResponseBody
				attempts, payloads := 0, 0
				opts := StreamOptions{Effort: "low", MaxRetries: new(0)}
				opts.Fetch = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					attempts++
					order = append(order, "fetch")
					if strings.HasPrefix(phase, "retry-") && attempts == 1 {
						return &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Invalid signature in thinking block"}}`))}, nil
					}
					if phase == "http" || phase == "retry-http" {
						return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"setup rejected"}}`))}, nil
					}
					body := &unreadResponseBody{}
					bodies = append(bodies, body)
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
				})}
				opts.OnPayload = func(any, *Model) (any, error) {
					payloads++
					order = append(order, "payload")
					if phase == "payload" || (phase == "retry-payload" && payloads == 2) {
						return nil, sentinel
					}
					return nil, nil
				}
				opts.OnResponse = func(context.Context, ProviderResponse, *Model) error {
					order = append(order, "response")
					return sentinel
				}
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), opts)
				result := requireAnthropicSetupError(t, stream, err)
				if result.API != APIAnthropicMessages || result.Provider != shape.provider || result.Model != cfg.Model || result.StopReason != StopReasonError {
					t.Fatalf("identity/state=%#v", result)
				}
				wantEffort := ""
				if shape.managed {
					wantEffort = "low"
				}
				if result.ProviderThinkingLevel != wantEffort || !strings.Contains(result.ErrorMessage, sentinel.Error()) {
					t.Fatalf("failure=%#v", result)
				}
				if phase == "payload" || phase == "response" || phase == "retry-payload" {
					if result.ErrorMessage != sentinel.Error() {
						t.Fatalf("callback error=%q", result.ErrorMessage)
					}
				}
				wantOrder := map[string][]string{"endpoint": nil, "payload": {"payload"}, "response": {"payload", "fetch", "response"}, "http": {"payload", "fetch"}, "retry-payload": {"payload", "fetch", "payload"}, "retry-http": {"payload", "fetch", "payload", "fetch"}}[phase]
				if !reflect.DeepEqual(order, wantOrder) {
					t.Fatalf("order=%v, want %v", order, wantOrder)
				}
				for _, body := range bodies {
					if body.reads.Load() != 0 || body.closes.Load() != 1 {
						t.Fatalf("rejected response body reads/closes=%d/%d", body.reads.Load(), body.closes.Load())
					}
				}
			})
		}
	}
}

func TestAnthropicSetupTransportFailure(t *testing.T) {
	for _, mode := range []string{"connection", "timeout", "abort"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				provider := NewAnthropicProvider(AnthropicConfig{Model: "custom-model", APIKey: "key", Compat: &ModelCompat{SupportsMidConvoEffort: new(true)}})
				defer func() { _ = provider.Close() }()
				opts := StreamOptions{Effort: "low", MaxRetries: new(0), Fetch: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if mode == "connection" {
						return nil, io.ErrUnexpectedEOF
					}
					if mode == "abort" {
						cancel()
					}
					<-request.Context().Done()
					return nil, request.Context().Err()
				})}}
				if mode == "timeout" {
					opts.TimeoutMs = new(1234)
				}
				start := time.Now()
				stream, err := provider.Stream(ctx, NormalizeContext(Context{}), opts)
				result := requireAnthropicSetupError(t, stream, err)
				wantMessage := map[string]string{"connection": "Connection error.", "timeout": "Request timed out.", "abort": "Request aborted"}[mode]
				wantReason := StopReasonError
				if mode == "abort" {
					wantReason = StopReasonAborted
				}
				if result.ErrorMessage != wantMessage || result.StopReason != wantReason || result.ProviderThinkingLevel != "low" {
					t.Fatalf("transport result=%#v", result)
				}
				if mode == "timeout" && (time.Since(start) != 1234*time.Millisecond || ctx.Err() != nil) {
					t.Fatalf("timeout elapsed=%s parent=%v", time.Since(start), ctx.Err())
				}
			})
		})
	}
}

func BenchmarkAnthropicSetupFailureWithHistory(b *testing.B) {
	provider := NewAnthropicProvider(AnthropicConfig{Model: "custom-model", ProviderID: "custom-proxy", APIKey: "key", Compat: &ModelCompat{SupportsMidConvoEffort: new(true)}})
	defer func() { _ = provider.Close() }()
	messages := make([]Message, 0, 128)
	for range 64 {
		messages = append(messages, UserMessage{Content: UserText("Read the current file and explain the result.")}, AssistantMessage{API: APIAnthropicMessages, Provider: "custom-proxy", Model: "custom-model", Content: []AssistantContentBlock{TextContent{Text: "The file contains a configured tool result."}}, ProviderThinkingLevel: "high", StopReason: StopReasonStop})
	}
	transcript := NormalizeContext(Context{Messages: messages})
	opts := StreamOptions{Effort: "low", OnPayload: func(any, *Model) (any, error) { return nil, errors.New("payload captured") }}
	b.ReportAllocs()
	for b.Loop() {
		stream, err := provider.Stream(b.Context(), transcript, opts)
		if err != nil {
			b.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != StopReasonError || result.ProviderThinkingLevel != "low" {
			b.Fatalf("result=%#v", result)
		}
	}
}

func TestAnthropicSetupCancellationAwaitsResponseObserver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := &unreadResponseBody{}
		provider := NewAnthropicProvider(AnthropicConfig{Model: "custom-model", ProviderID: "custom-proxy", APIKey: "key", BaseURL: "https://example.invalid", Compat: &ModelCompat{SupportsMidConvoEffort: new(true)}})
		defer func() { _ = provider.Close() }()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		entered := make(chan struct{})
		// Upstream stream() returns before its request settles (anthropic-messages.ts:511-830), so the Event Stream exists while the response observer runs; its result settles only after the callback completes.
		stream, err := provider.Stream(ctx, NormalizeContext(Context{}), StreamOptions{Effort: "low", Fetch: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
		})}, OnResponse: func(ctx context.Context, _ ProviderResponse, _ *Model) error {
			close(entered)
			<-ctx.Done()
			return errors.New("observer cancelled")
		}})
		if err != nil || stream == nil {
			t.Fatalf("Stream returned stream=%p err=%v", stream, err)
		}
		finished := make(chan *AssistantMessage)
		go func() { finished <- stream.Result() }()
		<-entered
		synctest.Wait()
		select {
		case <-finished:
			t.Fatal("setup settled before callback completion")
		default:
		}
		if body.reads.Load() != 0 || body.closes.Load() != 0 {
			t.Fatal("response consumed or closed while callback owns it")
		}
		cancel()
		completed := <-finished
		result := requireAnthropicSetupError(t, stream, nil)
		if completed != result {
			t.Fatalf("result=%p, want the stream's result %p", completed, result)
		}
		if result.StopReason != StopReasonAborted || result.ProviderThinkingLevel != "low" || result.ErrorMessage != "observer cancelled" {
			t.Fatalf("cancelled result=%#v", result)
		}
		if body.reads.Load() != 0 || body.closes.Load() != 1 {
			t.Fatalf("body reads/closes=%d/%d", body.reads.Load(), body.closes.Load())
		}
	})
}
