package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

const googleTestChunk = `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"one"}]}}],"responseId":"g"}` + "\r\n\r\n"
const googleTestFinal = `data: {"candidates":[{"content":{"role":"model","parts":[{"text":" two"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13}}` + "\r\n\r\n"

func runGoogleStream(t *testing.T, ctx context.Context, handler http.HandlerFunc, onEvent ...func(AssistantMessageEvent)) (*AssistantMessage, []AssistantMessageEvent) {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	provider := NewGoogleProvider(GoogleConfig{BaseURL: server.URL, APIKey: "test", Model: "gemini-2.5-flash", ProviderID: "google"})
	defer provider.(*googleProvider).client.CloseIdleConnections()
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var events []AssistantMessageEvent
	for event := range stream.Events(context.WithoutCancel(ctx)) {
		events = append(events, event)
		for _, hook := range onEvent {
			hook(event)
		}
	}
	return stream.Result(), events
}

func googleEventTypes(events []AssistantMessageEvent) string {
	types := make([]string, len(events))
	for i, event := range events {
		types[i] = string(event.EventType())
	}
	return strings.Join(types, " ")
}

// TestGoogleStreamFailuresFollowThePiLoop pins the failure paths of the SDK pipeline and the provider loop. Pi pushes start before it reads the body; an exception inside the loop reaches the catch, which pushes only the error (open blocks are not ended); after a clean loop the open blocks end before the abort and stop-reason checks.
// upstream: packages/ai/src/api/google-generative-ai.ts:100-108, 237-290; @google/genai 2.21.0 dist/node/index.mjs:13780-13860
func TestGoogleStreamFailuresFollowThePiLoop(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantTypes  string
		wantStop   StopReason
		wantError  string
		wantBlocks int
	}{
		{
			name:       "malformed record rejects json() after the first record was delivered",
			body:       googleTestChunk + "data: {bad json\r\n\r\n" + googleTestFinal,
			wantTypes:  "start text_start text_delta error",
			wantStop:   StopReasonError,
			wantError:  "google: invalid SSE JSON",
			wantBlocks: 1,
		},
		{
			name:      "body ends inside a record",
			body:      googleTestChunk + `data: {"candidates":[`,
			wantTypes: "start text_start text_delta error",
			wantStop:  StopReasonError,
			wantError: "Incomplete JSON segment at the end",
		},
		{
			name:      "raw error envelope is an ApiError",
			body:      `{"error":{"code":503,"message":"The model is overloaded","status":"UNAVAILABLE"}}`,
			wantTypes: "start error",
			wantStop:  StopReasonError,
			wantError: `got status: UNAVAILABLE. {"error":{"code":503,"message":"The model is overloaded","status":"UNAVAILABLE"}}`,
		},
		{
			name:      "no finish reason ends the clean loop: blocks end, then the error",
			body:      googleTestChunk,
			wantTypes: "start text_start text_delta text_end error",
			wantStop:  StopReasonError,
			wantError: "Google stream ended without a finish reason",
		},
		{
			name:      "event without a data: prefix is skipped",
			body:      "event: ping\r\n\r\n" + googleTestChunk + googleTestFinal,
			wantTypes: "start text_start text_delta text_delta text_end done",
			wantStop:  StopReasonStop,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, events := runGoogleStream(t, t.Context(), func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.body)
			})
			if got := googleEventTypes(events); got != test.wantTypes {
				t.Fatalf("events = %s; want %s", got, test.wantTypes)
			}
			if result.StopReason != test.wantStop {
				t.Fatalf("stopReason = %s; want %s (%q)", result.StopReason, test.wantStop, result.ErrorMessage)
			}
			if !strings.Contains(result.ErrorMessage, test.wantError) {
				t.Fatalf("errorMessage = %q; want it to contain %q", result.ErrorMessage, test.wantError)
			}
		})
	}
}

// TestGoogleStreamReleasesItsGoroutines runs streams that finish, fail, are aborted before and during the body and are abandoned by their consumer, and requires that every coroutine, body reader and forwarder ends.
func TestGoogleStreamReleasesItsGoroutines(t *testing.T) {
	baseline := runtime.NumGoroutine()
	for round := range 20 {
		ctx, cancel := context.WithCancel(t.Context())
		switch round % 4 {
		case 0: // completes
			runGoogleStream(t, ctx, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = io.WriteString(w, googleTestChunk+googleTestFinal)
			})
		case 1: // malformed record
			runGoogleStream(t, ctx, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = io.WriteString(w, googleTestChunk+"data: {bad\r\n\r\n")
			})
		case 2: // aborted while the body is pending
			result, _ := runGoogleStream(t, ctx, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}, func(event AssistantMessageEvent) {
				if _, ok := event.(StartEvent); ok {
					cancel()
				}
			})
			if result.StopReason != StopReasonAborted || result.ErrorMessage != "This operation was aborted" {
				t.Errorf("aborted stream = %s %q", result.StopReason, result.ErrorMessage)
			}
		case 3: // aborted before the response is read
			cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			provider := NewGoogleProvider(GoogleConfig{BaseURL: server.URL, APIKey: "test", Model: "gemini-2.5-flash", ProviderID: "google"})
			defer provider.(*googleProvider).client.CloseIdleConnections()
			if _, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{}); err == nil {
				t.Error("stream after cancellation returned no error")
			}
			server.Close()
		}
		cancel()
	}
	waitForGoroutines(t, baseline)
}
