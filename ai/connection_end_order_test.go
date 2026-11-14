package ai

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// What a provider reports when the server ends the first request's connection without a response, by how it ends it and when: order A
// after it read the request body to its end, order B as soon as the request headers are parsed, while the client still writes a 1 MiB body.
//
// Pi's texts are the oracle: Pi 1.5.2 (node v24.22.2, openai 7.19.0, @anthropic-ai/sdk 0.129.0, @google/genai 2.32.0), each API module's
// real stream() with maxRetries 0 against a node:http server, per shape of connectionEndShapes, order (A, B) and request body (6 KiB,
// 1 MiB), 100 runs quiet and 40 beside 64 busy loops. Every row gave one text:
//   - socket.resetAndDestroy(): `Connection error.: read ECONNRESET` 2800 of 2800 over the five SDK shapes, `fetch failed: read ECONNRESET`
//     560 of 560 for google-generative-ai, `fetch failed` 560 of 560 for mistral-conversations;
//   - socket.end() and socket.destroy(), each: `Connection error.: other side closed (UND_ERR_SOCKET)` 2800 of 2800,
//     `fetch failed: other side closed (UND_ERR_SOCKET)` 560 of 560, `fetch failed` 560 of 560.
//
// Node reports the read's result in both orders because libuv reads a ready socket before it writes to it; socketFailure carries that order.
type connectionEnd struct {
	name string
	// afterBody is order A: the server reads the request body to its end first. Order B acts as soon as the request headers are parsed.
	afterBody bool
	end       func(*net.TCPConn)
	// detail is the cause Pi appends to the SDK's or fetch's message.
	detail string
}

const (
	connectionEndReset  = "read ECONNRESET"
	connectionEndClosed = "other side closed (UND_ERR_SOCKET)"
)

func connectionEnds() []connectionEnd {
	// socket.resetAndDestroy(): a reset, no FIN.
	reset := func(conn *net.TCPConn) {
		_ = conn.SetLinger(0)
		_ = conn.Close()
	}
	// socket.end(): the server's FIN; it keeps reading until the client closes.
	end := func(conn *net.TCPConn) {
		_ = conn.CloseWrite()
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
	}
	ends := []connectionEnd{
		{"reset after the request body", true, reset, connectionEndReset},
		{"reset at the request headers", false, reset, connectionEndReset},
		{"FIN after the request body", true, end, connectionEndClosed},
		{"FIN at the request headers", false, end, connectionEndClosed},
		// socket.destroy() once the request is read: close() with nothing unread sends a FIN.
		{"close after the request body", true, func(conn *net.TCPConn) { _ = conn.Close() }, connectionEndClosed},
	}
	return append(ends, platformConnectionEnds()...)
}

// serveConnectionEnd ends the connection of every request as end says, and counts the requests.
func serveConnectionEnd(t *testing.T, end connectionEnd) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if end.afterBody {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Errorf("request body: %v", err)
				return
			}
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server does not support hijacking")
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		tcp, ok := connection.(*net.TCPConn)
		if !ok {
			t.Errorf("connection is %T, want *net.TCPConn", connection)
			_ = connection.Close()
			return
		}
		end.end(tcp)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// connectionEndShape is one provider shape of the oracle: the API module that Pi's stream() is, the credential and the base URL form.
type connectionEndShape struct {
	name     string
	streams  func() *ProviderStreams
	api      API
	provider string
	model    string
	key      string
	path     string
	// text is Pi's error message for a cause detail.
	text func(detail string) string
}

// The Stainless SDKs (openai, @anthropic-ai/sdk) wrap the fetch rejection in "Connection error."; withCauseDetail appends its cause.
func sdkConnectionError(detail string) string { return "Connection error.: " + detail }

func connectionEndShapes() []connectionEndShape {
	shapes := []connectionEndShape{
		{"anthropic API key", AnthropicMessagesAPI, APIAnthropicMessages, "anthropic", "claude-sonnet-4-20250514", "test-key", "", sdkConnectionError},
		{"anthropic OAuth token", AnthropicMessagesAPI, APIAnthropicMessages, "anthropic", "claude-sonnet-4-20250514", "sk-ant-oat-test-token", "", sdkConnectionError},
		{"openai-completions custom base URL", OpenAICompletionsAPI, APIOpenAICompletions, "my-gateway", "gateway-model", "test-key", "/v1", sdkConnectionError},
		{"openai-responses API key", OpenAIResponsesAPI, APIOpenAIResponses, "openai", "gpt-test", "test-key", "/v1", sdkConnectionError},
		{"openai-responses custom base URL", OpenAIResponsesAPI, APIOpenAIResponses, "my-gateway", "gateway-model", "test-key", "/v1", sdkConnectionError},
		// @google/genai rejects with fetch's own error; google-generative-ai.ts:290 formats it with formatProviderError(normalizeProviderError(error)).
		{"google-generative-ai", GoogleGenerativeAIAPI, APIGoogleGenerativeAI, "google", "gemini-2.5-flash", "test-key", "/v1beta", func(detail string) string { return "fetch failed: " + detail }},
	}
	return append(shapes, connectionEndMistralShapes()...)
}

// result streams one request with prompt as the user's text through the shape's API module, as Pi's stream() does, and returns its message.
func (shape connectionEndShape) result(t *testing.T, baseURL, prompt string) AssistantMessage {
	t.Helper()
	model := &Model{ID: shape.model, DisplayName: shape.model, Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: shape.api, ProviderID: shape.provider, BaseURL: baseURL + shape.path}, Capabilities: ModelCapabilities{ContextWindow: 128_000, MaxOutputTokens: 4096}}
	stream, err := shape.streams().Stream(context.Background(), model, newTranscriptContext([]Message{UserMessage{Content: UserText(prompt), Timestamp: 1}}), StreamOptions{APIKey: shape.key, MaxRetries: new(0)})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(t.Context()) {
	}
	return *stream.Result()
}

// Every row: the provider's message is Pi's text, the retry loop schedules one retry for that text, and the retry reaches the server (two requests).
func TestProviderReportsAConnectionTheServerEndsAsPi(t *testing.T) {
	prompt := strings.Repeat("x", 1<<20)
	policy := RetryPolicy{Enabled: true, MaxRetries: 1, BaseDelayMs: 1}
	for _, shape := range connectionEndShapes() {
		for _, end := range connectionEnds() {
			t.Run(shape.name+"/"+end.name, func(t *testing.T) {
				server, requests := serveConnectionEnd(t, end)
				want := shape.text(end.detail)
				var scheduled []string
				response, err := RetryAssistantCall(t.Context(), func() (AssistantMessage, error) {
					return shape.result(t, server.URL, prompt), nil
				}, &policy, RetryCallbacks{OnRetryScheduled: func(_, _, _ int, errorMessage string) error {
					scheduled = append(scheduled, errorMessage)
					return nil
				}})
				if err != nil {
					t.Fatal(err)
				}
				if response.StopReason != StopReasonError || response.ErrorMessage != want {
					t.Errorf("result = reason %q error %q, want error %q", response.StopReason, response.ErrorMessage, want)
				}
				if len(scheduled) != 1 || scheduled[0] != want {
					t.Errorf("retries scheduled for %q, want one for %q", scheduled, want)
				}
				if got := requests.Load(); got != 2 {
					t.Errorf("server requests = %d, want 2 (the request and one retry)", got)
				}
			})
		}
	}
}
