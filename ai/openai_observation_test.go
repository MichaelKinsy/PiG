package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func observationProvider(t *testing.T, api API, baseURL string) Provider {
	t.Helper()
	switch api {
	case APIOpenAICompletions:
		return NewOpenAIProvider(OpenAIConfig{Model: "probe", ProviderID: "probe-provider", APIKey: "test", BaseURL: baseURL})
	case APIAzureOpenAIResponses:
		return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{Model: "probe", APIKey: "test", BaseURL: baseURL})
	case APIOpenAICodexResponses:
		return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{Model: "probe", APIKey: codexTestToken(t, "acct"), BaseURL: baseURL})
	default:
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: "probe", ProviderID: "probe-provider", APIKey: "test", BaseURL: baseURL})
	}
}

func observationFinalBody(api API) string {
	if api == APIOpenAICompletions {
		return "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	}
	return "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-probe\",\"status\":\"completed\"}}\n\n"
}

// Pi admits start after successful response observation, before awaiting body data (openai-completions.ts:378-379,553; openai-responses.ts:177-180; azure-openai-responses.ts:130-131; openai-codex-responses.ts:473).
func TestOpenAIStartBeforeBodyData(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses, APIOpenAICodexResponses} {
		for _, abort := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/abort=%t", api, abort), func(t *testing.T) {
				release := make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				handlerDone := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(handlerDone)
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					select {
					case <-release:
						_, _ = fmt.Fprint(w, observationFinalBody(api))
					case <-r.Context().Done():
					}
				}))
				defer server.Close()
				defer unblock()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				provider := observationProvider(t, api, server.URL)
				defer func() { _ = provider.Close() }()
				stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{Transport: TransportSSE})
				if err != nil {
					t.Fatal(err)
				}
				observation, stop := context.WithTimeout(t.Context(), time.Second)
				defer stop()
				var first AssistantMessageEvent
				for event := range stream.Events(observation) {
					first = event
					break
				}
				if abort {
					cancel()
				} else {
					unblock()
				}
				var terminal *AssistantMessage
				for event := range stream.Events(t.Context()) {
					switch value := event.(type) {
					case DoneEvent:
						terminal = value.Message
					case ErrorEvent:
						terminal = value.Error
					}
				}
				<-handlerDone
				wantReason := StopReasonStop
				if abort {
					wantReason = StopReasonAborted
				}
				if result := stream.Result(); result != terminal || result.StopReason != wantReason {
					t.Fatalf("result=%#v terminal=%p, want %s", result, terminal, wantReason)
				}
				start, ok := first.(StartEvent)
				if !ok {
					t.Fatalf("start depends on body data: first=%#v", first)
				}
				if len(start.Partial.Content) != 0 || start.Partial.StopReason != StopReasonPending {
					t.Fatalf("header-only partial=%#v", start.Partial)
				}
			})
		}
	}
}

// Pi returns the Event Stream before its async request settles (openai-completions.ts:299-307,365-379; openai-responses.ts:113-122,164-180).
func TestOpenAIStreamReturnsBeforeResponseHeaders(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			returned := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(returned) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				select {
				case <-returned:
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, observationFinalBody(api))
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer unblock()
			provider := observationProvider(t, api, server.URL)
			defer func() { _ = provider.Close() }()
			// This deadline detects a cyclic wait; it never releases headers or changes the expected observation order.
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{})
			unblock()
			if err != nil {
				t.Fatalf("Stream waited for headers before returning its Event Stream: %v", err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatalf("header-return handshake failed: %#v", result)
			}
		})
	}
}

// Pi awaits onResponse before start, and setup rejection cannot publish a successful start (openai-completions.ts:378-379; openai-responses.ts:177-178).
func TestOpenAIStartRequiresSuccessfulResponseSetup(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
			t.Run(fmt.Sprintf("%s/status=%d", api, status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.WriteHeader(status)
					_, _ = fmt.Fprint(w, observationFinalBody(api))
				}))
				defer server.Close()
				provider := observationProvider(t, api, server.URL)
				defer func() { _ = provider.Close() }()
				called := false
				rejected := errors.New("response observer rejected setup")
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{}), StreamOptions{OnResponse: func(context.Context, ProviderResponse, *Model) error {
					called = true
					return rejected
				}})
				if err != nil || stream == nil {
					t.Fatalf("async setup returned stream=%p err=%v", stream, err)
				}
				var events []AssistantEventType
				for event := range stream.Events(t.Context()) {
					events = append(events, event.EventType())
				}
				result := stream.Result()
				if len(events) != 1 || events[0] != EventError || result.StopReason != StopReasonError {
					t.Fatalf("failed setup fabricated start: %v %#v", events, result)
				}
				if called != (status == http.StatusOK) || (called && result.ErrorMessage != rejected.Error()) {
					t.Fatalf("response hook called=%t error=%s", called, result.ErrorMessage)
				}
			})
		}
	}
}
