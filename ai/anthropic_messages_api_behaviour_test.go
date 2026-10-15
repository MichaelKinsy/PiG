package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const anthropicAPITestSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_api\",\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"pong\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func anthropicAPITestModel(baseURL string) *Model {
	return &Model{ID: "claude-test", DisplayName: "claude-test", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "anthropic", BaseURL: baseURL}, Capabilities: ModelCapabilities{ContextWindow: 1000}}
}

func drainAnthropicAPIStream(t *testing.T, stream *AssistantMessageEventStream) (*AssistantMessage, []AssistantEventType) {
	t.Helper()
	var types []AssistantEventType
	for event := range stream.Events(t.Context()) {
		types = append(types, event.EventType())
	}
	return stream.Result(), types
}

// compat.ts:181 and legacy-api-aliases.ts register anthropicMessagesApi() (api/anthropic-messages.lazy.ts) as the "anthropic-messages" streams. Its stream and streamSimple return the stream at once (api/lazy.ts lazyStream) and run anthropic-messages.ts stream (:600-700) behind it: the request goes to the model's base URL with the key in x-api-key, and the server's events end in the finished message.
func TestAnthropicMessagesAPIStreamsTheModelsRequestAndReturnsTheMessage(t *testing.T) {
	for _, simple := range []bool{false, true} {
		var gotKey, gotPath atomic.Value
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotKey.Store(r.Header.Get("x-api-key"))
			gotPath.Store(r.URL.Path)
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, anthropicAPITestSSE)
		}))
		run := AnthropicMessagesAPI().Stream
		if simple {
			run = AnthropicMessagesAPI().StreamSimple
		}
		stream, err := run(t.Context(), anthropicAPITestModel(server.URL), newTranscriptContext([]Message{UserMessage{Content: UserText("ping"), Timestamp: 1}}), StreamOptions{APIKey: "sk-ant-api-test"})
		if err != nil {
			t.Fatalf("simple=%v: %v", simple, err)
		}
		message, types := drainAnthropicAPIStream(t, stream)
		server.Close()
		if message.StopReason != StopReasonStop || message.ErrorMessage != "" || len(message.Content) != 1 || message.Content[0].(TextContent).Text != "pong" {
			t.Fatalf("simple=%v: message = stop %q error %q content %#v", simple, message.StopReason, message.ErrorMessage, message.Content)
		}
		if message.API != APIAnthropicMessages || message.Provider != "anthropic" || message.Model != "claude-test" || message.ResponseID != "msg_api" {
			t.Fatalf("simple=%v: message identity = %q/%q/%q response %q", simple, message.API, message.Provider, message.Model, message.ResponseID)
		}
		if types[0] != EventStart || types[len(types)-1] != EventDone {
			t.Fatalf("simple=%v: events = %v", simple, types)
		}
		if gotKey.Load() != "sk-ant-api-test" || !strings.HasSuffix(gotPath.Load().(string), "/v1/messages") {
			t.Fatalf("simple=%v: request key %v path %v", simple, gotKey.Load(), gotPath.Load())
		}
	}
}

// api/lazy.ts lazyStream: a setup failure ends the returned stream with an error event instead of failing the call. anthropic-messages.ts stream asserts the request authentication inside its try block (:611-613), so a missing key ends the stream with stop reason "error" and, with an aborted signal, "aborted" (`options?.signal?.aborted ? "aborted" : "error"`). streamSimple asserts it before building the stream (:933-935), a setup failure that lazyStream reports as an error even for an aborted signal. Neither sends a request.
func TestAnthropicMessagesAPIMissingKeyEndsTheStreamAndSendsNoRequest(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"} {
		t.Setenv(name, "")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, probe := range []struct {
		simple  bool
		aborted bool
		want    StopReason
	}{{false, false, StopReasonError}, {false, true, StopReasonAborted}, {true, false, StopReasonError}, {true, true, StopReasonError}} {
		run := AnthropicMessagesAPI().Stream
		if probe.simple {
			run = AnthropicMessagesAPI().StreamSimple
		}
		ctx, cancel := context.WithCancel(t.Context())
		if probe.aborted {
			cancel()
		}
		stream, err := run(ctx, anthropicAPITestModel(server.URL), newTranscriptContext([]Message{UserMessage{Content: UserText("ping"), Timestamp: 1}}), StreamOptions{})
		if err != nil {
			t.Fatalf("%+v: the lazy API returns the stream at once, got error %v", probe, err)
		}
		message, types := drainAnthropicAPIStream(t, stream)
		cancel()
		if message.StopReason != probe.want || message.ErrorMessage == "" || len(message.Content) != 0 || types[len(types)-1] != EventError {
			t.Fatalf("%+v: stop %q error %q content %d events %v", probe, message.StopReason, message.ErrorMessage, len(message.Content), types)
		}
		if message.API != APIAnthropicMessages || message.Provider != "anthropic" || message.Model != "claude-test" {
			t.Fatalf("%+v: identity %q/%q/%q", probe, message.API, message.Provider, message.Model)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("a missing key sent %d requests", requests.Load())
	}
}

// Pi's model parameter is a required object, so the Go lazy API fails the call for a nil model rather than returning a stream.
func TestAnthropicMessagesAPIRejectsANilModel(t *testing.T) {
	for _, run := range []func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error){AnthropicMessagesAPI().Stream, AnthropicMessagesAPI().StreamSimple} {
		if stream, err := run(t.Context(), nil, TranscriptContext{}, StreamOptions{}); err == nil || stream != nil {
			t.Fatalf("nil model returned stream %v error %v", stream, err)
		}
	}
}
