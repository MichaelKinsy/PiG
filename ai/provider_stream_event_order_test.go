package ai

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// queuedTextDeltas lists the text deltas pushed to s and not yet consumed.
func queuedTextDeltas(s *AssistantMessageEventStream) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, event := range s.queue {
		if delta, ok := event.(TextDeltaEvent); ok {
			out = append(out, delta.Delta)
		}
	}
	return out
}

// longDelta is the first text delta. Its size keeps the parser busy normalizing it, so an observer that runs ahead of the parser sees it missing.
var longDelta = strings.Repeat("a", 1<<16)

// assertObserverFollowsNormalization streams two text deltas, longDelta then "b", without consuming the stream. Upstream awaits the observer for an event only
// when the adapter pulls that event, after it pushed everything the previous event produced (for example openai-responses-shared.ts:600-601,
// openai-codex-responses.ts:749-751), so the observer for "b" must find longDelta already pushed and the delta for "b" not yet pushed.
func assertObserverFollowsNormalization(t *testing.T, start func(OnProviderStreamEvent func(context.Context, any, *Model) error) (*AssistantMessageEventStream, error), isB func(map[string]any) bool) {
	t.Helper()
	for iteration := range 20 {
		streams := make(chan *AssistantMessageEventStream, 1)
		var atB []string
		observedB := false
		observe := func(_ context.Context, data any, _ *Model) error {
			event, _ := data.(map[string]any)
			if event == nil || !isB(event) {
				return nil
			}
			observedB = true
			select {
			case stream := <-streams:
				atB = queuedTextDeltas(stream)
				streams <- stream
			case <-time.After(10 * time.Second):
				return errors.New("stream was never returned")
			}
			return nil
		}
		stream, err := start(observe)
		if err != nil {
			t.Fatal(err)
		}
		streams <- stream
		result := stream.Result()
		if result.StopReason != StopReasonStop || !observedB {
			t.Fatalf("iteration %d: observed b=%v result=%#v", iteration, observedB, result)
		}
		if len(atB) != 1 || atB[0] != longDelta {
			t.Fatalf("iteration %d: observer for b ran with %d text deltas pushed, want only the first", iteration, len(atB))
		}
	}
}

// drainCodexWSClosed consumes the peer's connection-closed notices, which a test that opens more connections than the channel holds never reads.
func drainCodexWSClosed(t *testing.T, peer *codexWSPeer) {
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-peer.closed:
			case <-done:
				return
			}
		}
	}()
}

func TestProviderStreamEventObserverFollowsNormalization(t *testing.T) {
	responsesWire := []string{
		`{"type":"response.created","response":{"id":"resp_order"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"` + longDelta + `"}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"b"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"` + longDelta + `b"}]}}`,
		codexWSTerminal("resp_order"),
	}
	responsesB := func(event map[string]any) bool { return event["delta"] == "b" }

	// The Codex WebSocket feeder reads frames on its own goroutine, so it must hold each observation until the parser pulls that frame.
	t.Run("openai-codex-responses websocket, OAuth token", func(t *testing.T) {
		peer := newCodexWSPeer(t, func(int, int, map[string]any) []string { return responsesWire })
		drainCodexWSClosed(t, peer)
		provider := peer.provider(t, "acc_test")
		assertObserverFollowsNormalization(t, func(observe func(context.Context, any, *Model) error) (*AssistantMessageEventStream, error) {
			return provider.Stream(t.Context(), codexUpstreamContext(), StreamOptions{Transport: TransportWebSocket, OnProviderStreamEvent: observe})
		}, responsesB)
	})
	t.Run("openai-codex-responses SSE, OAuth token", func(t *testing.T) {
		peer := newCodexWSPeer(t, func(int, int, map[string]any) []string { return responsesWire })
		provider := peer.provider(t, "acc_test")
		assertObserverFollowsNormalization(t, func(observe func(context.Context, any, *Model) error) (*AssistantMessageEventStream, error) {
			return provider.Stream(t.Context(), codexUpstreamContext(), StreamOptions{Transport: TransportSSE, OnProviderStreamEvent: observe, Fetch: sseResponse(sseFrames(responsesWire...))})
		}, responsesB)
	})
	t.Run("openai-responses, API key", func(t *testing.T) {
		provider, err := directAPIProvider(chatGPTTestModel("https://api.openai.com/v1", nil), "sk-test", nil)
		if err != nil {
			t.Fatal(err)
		}
		assertObserverFollowsNormalization(t, func(observe func(context.Context, any, *Model) error) (*AssistantMessageEventStream, error) {
			return provider.Stream(t.Context(), responsesUpstreamContext(), StreamOptions{OnProviderStreamEvent: observe, Fetch: sseResponse(sseFrames(responsesWire...))})
		}, responsesB)
	})
	t.Run("openai-completions, custom base URL without a default model", func(t *testing.T) {
		a := `{"id":"chatcmpl-order","model":"local-model","choices":[{"index":0,"delta":{"content":"` + longDelta + `"}}]}`
		b := `{"id":"chatcmpl-order","model":"local-model","choices":[{"index":0,"delta":{"content":"b"}}]}`
		stop := `{"id":"chatcmpl-order","model":"local-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
		server := serveSSE(t, sseFrames(a, b, stop, "[DONE]"))
		model := &Model{ID: "local-model", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "local-compatible", BaseURL: server.URL}}
		assertObserverFollowsNormalization(t, func(observe func(context.Context, any, *Model) error) (*AssistantMessageEventStream, error) {
			return StreamSimple(t.Context(), model, helloTranscript(), StreamOptions{APIKey: "local-key", OnProviderStreamEvent: observe})
		}, func(event map[string]any) bool {
			choices, _ := event["choices"].([]any)
			if len(choices) == 0 {
				return false
			}
			delta, _ := choices[0].(map[string]any)["delta"].(map[string]any)
			return delta["content"] == "b"
		})
	})
}

// mapCodexEvents observes every WebSocket event, including the leading codex.rate_limits event and an error event that makes the request retry on a new
// connection (openai-codex-responses.ts:749-757, 342-352).
func TestCodexWebSocketObservesFramesThatPrecedeARetry(t *testing.T) {
	rateLimits := `{"type":"codex.rate_limits","plan_type":"plus","rate_limits":{"allowed":true,"limit_reached":false}}`
	limit := `{"type":"error","error":{"code":"websocket_connection_limit_reached","message":"limit"}}`
	hello := codexWSHello("resp_retry", "Hello")
	peer := newCodexWSPeer(t, func(index, _ int, _ map[string]any) []string {
		if index == 1 {
			return []string{rateLimits, limit}
		}
		return hello
	})
	recorder := &providerEventRecorder{}
	result, _ := codexWSResult(t, peer.provider(t, "acc_test"), codexUpstreamContext(), StreamOptions{Transport: TransportWebSocket, OnProviderStreamEvent: recorder.observe})
	if result.StopReason != StopReasonStop || codexUpstreamText(result) != "Hello" {
		t.Fatalf("result = %#v", result)
	}
	events, _ := recorder.snapshot()
	if want := decodedEvents(t, append([]string{rateLimits, limit}, hello...)...); !reflect.DeepEqual(events, want) {
		t.Fatalf("observed events = %#v, want %#v", events, want)
	}
}

// A failing observer on a leading codex.rate_limits event is a ProviderStreamEventCallbackError: no WebSocket retry and no SSE fallback.
func TestCodexWebSocketLeadingEventObserverFailureSkipsTransportRecovery(t *testing.T) {
	rateLimits := `{"type":"codex.rate_limits","plan_type":"plus","rate_limits":{"allowed":true,"limit_reached":false}}`
	peer := newCodexWSPeer(t, func(int, int, map[string]any) []string {
		return append([]string{rateLimits}, codexWSHello("resp_1", "Hello")...)
	})
	callbackFailure := errors.New("observer rejected the rate limits")
	result, types := codexWSResult(t, peer.provider(t, "acc_test"), codexUpstreamContext(), StreamOptions{Transport: TransportAuto, SessionID: "leading-callback-failure", OnProviderStreamEvent: func(_ context.Context, data any, _ *Model) error {
		if data.(map[string]any)["type"] == "codex.rate_limits" {
			return callbackFailure
		}
		return nil
	}})
	headers, bodies, fetches := peer.snapshot()
	if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, callbackFailure.Error()) || !reflect.DeepEqual(types, []AssistantEventType{EventError}) {
		t.Fatalf("result = %#v events = %v", result, types)
	}
	if len(headers) != 1 || len(bodies) != 1 || fetches != 0 {
		t.Fatalf("connections=%d requests=%d sse fetches=%d, want one WebSocket request and no fallback", len(headers), len(bodies), fetches)
	}
}
