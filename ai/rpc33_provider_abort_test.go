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
	"testing/iotest"
)

func providerAbortPrefix(api API, shape string) (string, AssistantEventType, string) {
	if shape == "start" {
		return "", EventStart, `[]`
	}
	if api == APIOpenAICompletions {
		switch shape {
		case "text":
			return "data: {\"choices\":[{\"delta\":{\"content\":\"retained\"}}]}\n\n", EventTextDelta, `[{"type":"text","text":"retained"}]`
		case "function":
			return completionToolFrame(`[{"id":"call","index":0,"function":{"name":"run","arguments":"{\"input\":\"retained"}}]`, `null`), EventToolCallDelta, `[{"type":"toolCall","id":"call","name":"run","arguments":{"input":"retained"}}]`
		default:
			return completionToolFrame(`[{"id":"call","index":0,"custom":{"name":"run","input":"retained"}}]`, `null`), EventToolCallDelta, `[{"type":"toolCall","id":"call","name":"run","arguments":{"input":"retained"}}]`
		}
	}
	switch shape {
	case "text":
		return responseScratchFrame("output_item.added", `"output_index":0,"item":{"type":"message","id":"message","content":[]}`) + responseScratchFrame("output_text.delta", `"output_index":0,"delta":"retained"`), EventTextDelta, `[{"type":"text","text":"retained"}]`
	case "function":
		return responseScratchFrame("output_item.added", `"output_index":0,"item":{"type":"function_call","id":"item","call_id":"call","name":"run","arguments":""}`) + responseScratchFrame("function_call_arguments.delta", `"output_index":0,"delta":"{\"input\":\"retained"`), EventToolCallDelta, `[{"type":"toolCall","id":"call|item","name":"run","arguments":{"input":"retained"}}]`
	default:
		return responseScratchFrame("output_item.added", `"output_index":0,"item":{"type":"custom_tool_call","id":"item","call_id":"call","name":"run","input":""}`) + responseScratchFrame("custom_tool_call_input.delta", `"output_index":0,"delta":"retained"`), EventToolCallDelta, `[{"type":"toolCall","id":"call|item","name":"run","arguments":{"input":"retained"}}]`
	}
}

func TestOpenAIProviderAbortAfterAdmission(t *testing.T) {
	// upstream: packages/ai/src/api/openai-completions.ts:682-687,712-713
	// upstream: packages/ai/src/api/openai-responses-shared.ts:758-759 rejects a body ending before a terminal event.
	// upstream: packages/ai/src/api/openai-responses.ts:206-211 preserves that rejection's text while marking the result aborted.
	// The raw Pi RPC33 oracle distinguishes Completions' explicit abort from Responses' missing-terminal rejection.
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		for _, shape := range []string{"start", "text", "function", "grammar"} {
			t.Run(string(api)+"/"+shape, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(t.Context())
				prefix, admitted, wantContent := providerAbortPrefix(api, shape)
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, prefix)
					w.(http.Flusher).Flush()
					<-request.Context().Done()
				}))
				defer server.Close()
				defer cancel()
				var provider Provider
				if api == APIOpenAICompletions {
					provider = NewOpenAIProvider(OpenAIConfig{ProviderID: "custom-proxy", Model: "no-catalog-model", BaseURL: server.URL, APIKey: "test"})
				} else {
					provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{ProviderID: "custom-proxy", Model: "no-catalog-model", BaseURL: server.URL, APIKey: "test"})
				}
				defer func() { _ = provider.Close() }()
				stream, err := provider.Stream(ctx, piMessagesTestContext(), StreamOptions{MaxRetries: new(0)})
				if err != nil {
					t.Fatal(err)
				}
				var terminal *AssistantMessage
				var types []AssistantEventType
				canceled := false
				for event := range stream.Events(t.Context()) {
					types = append(types, event.EventType())
					if !canceled && event.EventType() == admitted {
						canceled = true
						cancel()
					}
					if failure, ok := event.(ErrorEvent); ok {
						if failure.Reason != StopReasonAborted {
							t.Errorf("terminal reason = %q", failure.Reason)
						}
						terminal = failure.Error
					}
				}
				if !canceled || len(types) == 0 || types[0] != EventStart || types[len(types)-1] != EventError {
					t.Fatalf("admission/cancellation events = %v", types)
				}
				result := stream.Result()
				if result != terminal {
					t.Fatal("terminal event and Result do not share final identity")
				}
				wantError := "Request was aborted"
				if api == APIOpenAIResponses {
					wantError = "OpenAI Responses stream ended before a terminal response event"
				}
				if result.StopReason != StopReasonAborted || result.ErrorMessage != wantError {
					t.Errorf("stop = %q, error = %q; want aborted / %s", result.StopReason, result.ErrorMessage, wantError)
				}
				assertScratchJSON(t, result.Content, wantContent)
				if requests.Load() != 1 {
					t.Errorf("request count = %d; want one admitted request", requests.Load())
				}
			})
		}
	}
}

func TestOpenAIProviderAbortAtParserReadBoundaries(t *testing.T) {
	// Both the post-read branch and the EOF branch are stream-body cancellation, not SDK request-setup errors.
	// Azure uses the shared Responses parser; Codex's raw reader retains its explicit abort rejection.
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses, APIOpenAICodexResponses} {
		for _, boundary := range []string{"next-event", "eof"} {
			t.Run(string(api)+"/"+boundary, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				prefix, _, wantContent := providerAbortPrefix(api, "function")
				frames := []string{prefix}
				if boundary == "next-event" {
					frames = append(frames, "data: {}\n\n")
				}
				reader := &providerScratchReader{frames: frames, beforeRead: func(i int) {
					if i == 1 {
						cancel()
					}
				}}
				builder := newAssistantStreamBuilder(ctx, api, "custom-proxy", "no-catalog-model")
				builder.start()
				if api == APIOpenAICompletions {
					(&openAIProvider{}).parseSSE(ctx, reader, builder, nil)
				} else {
					(&openAIResponsesProvider{cfg: OpenAIResponsesConfig{Codex: api == APIOpenAICodexResponses}}).parseResponsesSSE(ctx, reader, builder, nil)
				}
				result := builder.stream.Result()
				wantError := "Request was aborted"
				if api == APIOpenAIResponses || api == APIAzureOpenAIResponses {
					wantError = "OpenAI Responses stream ended before a terminal response event"
				}
				if result.StopReason != StopReasonAborted || result.ErrorMessage != wantError {
					t.Errorf("stop = %q, error = %q; want aborted / %s", result.StopReason, result.ErrorMessage, wantError)
				}
				assertScratchJSON(t, result.Content, wantContent)
			})
		}
	}
}

func TestOpenAIProviderAbortDoesNotRewriteUnrelatedErrors(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		for _, message := range []string{"context canceled", "upstream transport failed"} {
			t.Run(string(api)+"/"+message, func(t *testing.T) {
				t.Parallel()
				prefix, _, wantContent := providerAbortPrefix(api, "function")
				reader := io.MultiReader(strings.NewReader(prefix), iotest.ErrReader(errors.New(message)))
				builder := newAssistantStreamBuilder(t.Context(), api, "custom-proxy", "no-catalog-model")
				builder.start()
				if api == APIOpenAICompletions {
					(&openAIProvider{}).parseSSE(t.Context(), reader, builder, nil)
				} else {
					(&openAIResponsesProvider{}).parseResponsesSSE(t.Context(), reader, builder, nil)
				}
				result := builder.stream.Result()
				if result.StopReason != StopReasonError || result.ErrorMessage != message {
					t.Errorf("stop = %q, error = %q; want error / %q", result.StopReason, result.ErrorMessage, message)
				}
				assertScratchJSON(t, result.Content, wantContent)
			})
		}
	}
}
