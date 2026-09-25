package ai

// Codex transport regressions derived from pinned Pi 0.87.1 behavior in
// packages/ai/src/api/openai-codex-responses.ts. All servers are loopback
// httptest servers; no real network is used.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func reviewCodexProvider(t *testing.T, url, account string) Provider {
	t.Helper()
	return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
		APIKey:     codexTestToken(t, account),
		Model:      "gpt-5.2",
		ProviderID: "openai-codex",
		BaseURL:    url,
	})
}

func reviewCodexCtx(text string) TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText(text)}}})
}

const reviewSSEDone = "data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_sse\",\"status\":\"completed\"}}\n\n"

func reviewWriteCompleted(conn *websocket.Conn, id string) error {
	for _, event := range []map[string]any{
		{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_" + id, "status": "in_progress"}},
		{"type": "response.output_text.delta", "output_index": 0, "delta": "ok"},
		{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_" + id, "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "ok"}}}},
		{"type": "response.completed", "response": map[string]any{"id": "resp_" + id, "status": "completed"}},
	} {
		if err := conn.WriteJSON(event); err != nil {
			return err
		}
	}
	return nil
}

// Upstream openai-codex-responses.ts:344-352,361-374: a
// websocket_connection_limit_reached first event is retried once; when the
// retry hits the limit again, connectionLimitBeforeStart keeps the error out of
// the rethrow branch and the request falls back to SSE.
func TestCodexConnectionLimitTwiceFallsBackToSSE(t *testing.T) {
	var wsAttempts, sseAttempts atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			sseAttempts.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(reviewSSEDone))
			return
		}
		wsAttempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": map[string]any{"code": "websocket_connection_limit_reached", "message": "limit"}})
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()

	stream, err := reviewCodexProvider(t, server.URL, "acct_limit").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{Transport: TransportAuto, SessionID: "review-limit", TimeoutMs: 1000})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonStop || result.ResponseID != "resp_sse" {
		t.Fatalf("result stop=%q response=%q err=%q; want SSE fallback success (ws attempts=%d sse=%d)", result.StopReason, result.ResponseID, result.ErrorMessage, wsAttempts.Load(), sseAttempts.Load())
	}
	if wsAttempts.Load() != 2 || sseAttempts.Load() != 1 {
		t.Errorf("attempts ws=%d sse=%d, want 2 and 1", wsAttempts.Load(), sseAttempts.Load())
	}
}

// Upstream openai-codex-responses.ts:364-370 + 948-957: every transport
// failure, including one after the message stream started, calls
// recordWebSocketFailure, which adds the session to websocketSseFallbackSessions.
// The next request in that session goes straight to SSE (:291-294).
func TestCodexMidStreamWebSocketFailureMakesSessionFallBackToSSE(t *testing.T) {
	var wsAttempts, sseAttempts atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			sseAttempts.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(reviewSSEDone))
			return
		}
		wsAttempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			_ = conn.Close()
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_x", "status": "in_progress"}})
		_ = conn.WriteJSON(map[string]any{"type": "response.output_text.delta", "output_index": 0, "delta": "partial"})
		_ = conn.Close() // abrupt transport drop after output started
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()
	ResetOpenAICodexWebSocketDebugStats()

	provider := reviewCodexProvider(t, server.URL, "acct_mid")
	options := StreamOptions{Transport: TransportAuto, SessionID: "review-mid", TimeoutMs: 1000}
	first, err := provider.Stream(context.Background(), reviewCodexCtx("one"), options)
	if err != nil {
		t.Fatal(err)
	}
	if r := first.Result(); r.StopReason != StopReasonError {
		t.Fatalf("first stop=%q, want error after mid-stream drop", r.StopReason)
	}
	second, err := provider.Stream(context.Background(), reviewCodexCtx("two"), options)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Result()
	if wsAttempts.Load() != 1 || sseAttempts.Load() != 1 {
		t.Errorf("attempts ws=%d sse=%d, want ws=1 sse=1 (session fallback is sticky after a WebSocket failure)", wsAttempts.Load(), sseAttempts.Load())
	}
	if stats := GetOpenAICodexWebSocketDebugStats("review-mid"); stats == nil || stats.WebSocketFailures != 1 || !stats.WebSocketFallbackActive {
		t.Errorf("stats=%+v, want websocketFailures=1 fallbackActive=true", stats)
	}
}

// Upstream openai-codex-responses.ts:1120-1124,1257-1281: a cached socket the
// server has closed while idle is not reusable (readyState != OPEN), so the
// next request opens a fresh WebSocket. It must not be recorded as a
// WebSocket failure or switch the session to SSE.
func TestCodexIdleServerClosedCachedSocketReconnectsWebSocket(t *testing.T) {
	var wsAttempts, sseAttempts atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			sseAttempts.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(reviewSSEDone))
			return
		}
		n := wsAttempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = reviewWriteCompleted(conn, string(rune('0'+n)))
		// Server-side idle close after the response completes.
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "server idle"), time.Now().Add(time.Second))
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()
	ResetOpenAICodexWebSocketDebugStats()

	provider := reviewCodexProvider(t, server.URL, "acct_idle")
	options := StreamOptions{Transport: TransportAuto, SessionID: "review-idle", TimeoutMs: 1000}
	first, err := provider.Stream(context.Background(), reviewCodexCtx("one"), options)
	if err != nil {
		t.Fatal(err)
	}
	if r := first.Result(); r.StopReason != StopReasonStop {
		t.Fatalf("first stop=%q err=%q", r.StopReason, r.ErrorMessage)
	}
	time.Sleep(20 * time.Millisecond) // let the close frame arrive (Node would process the close event)
	second, err := provider.Stream(context.Background(), reviewCodexCtx("two"), options)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Result()
	if wsAttempts.Load() != 2 || sseAttempts.Load() != 0 {
		t.Errorf("attempts ws=%d sse=%d, want a fresh WebSocket (ws=2 sse=0)", wsAttempts.Load(), sseAttempts.Load())
	}
	if stats := GetOpenAICodexWebSocketDebugStats("review-idle"); stats != nil && stats.WebSocketFallbackActive {
		t.Errorf("stats=%+v: server idle close must not activate sticky SSE fallback", stats)
	}
}

// Upstream openai-codex-responses.ts:809-812 skips "[DONE]"; the shared
// processResponsesStream then throws "OpenAI Responses stream ended before a
// terminal response event" (openai-responses-shared.ts:758-760).
func TestCodexSSEDoneSentinelIsNotTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_x\"}}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	stream, err := reviewCodexProvider(t, server.URL, "acct_done").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{Transport: TransportSSE})
	if err != nil {
		t.Fatal(err)
	}
	if r := stream.Result(); r.StopReason != StopReasonError {
		t.Fatalf("stop=%q, want error: upstream Codex ignores [DONE] and requires a terminal response event", r.StopReason)
	}
}

// Upstream openai-codex-responses.ts:811-817: malformed Codex SSE JSON throws
// CodexProtocolError("Invalid Codex SSE JSON: ...") rather than being skipped.
func TestCodexSSEInvalidJSONFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {not json\n\n" + reviewSSEDone))
	}))
	defer server.Close()
	stream, err := reviewCodexProvider(t, server.URL, "acct_json").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{Transport: TransportSSE})
	if err != nil {
		t.Fatal(err)
	}
	r := stream.Result()
	if r.StopReason != StopReasonError || !strings.Contains(r.ErrorMessage, "Invalid Codex SSE JSON") {
		t.Fatalf("stop=%q err=%q, want Invalid Codex SSE JSON error", r.StopReason, r.ErrorMessage)
	}
}

func TestCodexSSEFramingAndErrorMapping(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		wantStop  StopReason
		wantError string
	}{
		{
			name:     "data field needs no space and may span lines",
			body:     "data:{\"type\":\ndata: \"response.done\",\"response\":{\"id\":\"resp_multiline\",\"status\":\"completed\"}}\n\n",
			wantStop: StopReasonStop,
		},
		{
			name:      "nested Codex error",
			body:      "data: {\"type\":\"error\",\"error\":{\"code\":\"bad\",\"message\":\"broken\"}}\n\n",
			wantStop:  StopReasonError,
			wantError: "Codex error: broken",
		},
		{
			name:      "response failed without details",
			body:      "data: {\"type\":\"response.failed\",\"response\":{}}\n\n",
			wantStop:  StopReasonError,
			wantError: "Codex response failed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			stream, err := reviewCodexProvider(t, server.URL, "acct_frames").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{Transport: TransportSSE})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if result.StopReason != test.wantStop {
				t.Fatalf("stop=%q error=%q, want %q", result.StopReason, result.ErrorMessage, test.wantStop)
			}
			if test.wantError != "" && !strings.Contains(result.ErrorMessage, test.wantError) {
				t.Errorf("error=%q, want %q", result.ErrorMessage, test.wantError)
			}
		})
	}
}

func TestCodexWebSocketInvalidJSONIsProtocolError(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		if _, _, err := connection.ReadMessage(); err != nil {
			return
		}
		_ = connection.WriteMessage(websocket.TextMessage, []byte("{bad"))
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()
	ResetOpenAICodexWebSocketDebugStats()
	stream, err := reviewCodexProvider(t, server.URL, "acct_ws_json").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{
		Transport: TransportAuto, SessionID: "ws-json", TimeoutMs: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, "Invalid Codex WebSocket JSON") {
		t.Fatalf("stop=%q error=%q, want WebSocket protocol error", result.StopReason, result.ErrorMessage)
	}
	if stats := GetOpenAICodexWebSocketDebugStats("ws-json"); stats != nil && stats.WebSocketFallbackActive {
		t.Errorf("stats=%+v: protocol errors must not activate transport fallback", stats)
	}
}

// Upstream openai-codex-responses.ts:1612-1636: buildBaseCodexHeaders /
// buildSSEHeaders set chatgpt-account-id, originator, OpenAI-Beta,
// session-id and x-client-request-id AFTER request headers, so callers cannot
// override or delete them.
func TestCodexSSEProtocolHeadersWinOverRequestHeaders(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(reviewSSEDone))
	}))
	defer server.Close()
	stream, err := reviewCodexProvider(t, server.URL, "acct_real").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{
		Transport: TransportSSE,
		SessionID: "sess",
		Headers: ProviderHeaders{
			"Authorization":      new("Bearer spoof"),
			"chatgpt-account-id": new("acct_spoof"),
			"session-id":         new("other"),
			"originator":         nil,
			"OpenAI-Beta":        nil,
			"Accept":             nil,
			"Content-Type":       nil,
			"Content-Encoding":   nil,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Result()
	if v := got.Get("chatgpt-account-id"); v != "acct_real" {
		t.Errorf("chatgpt-account-id=%q, want acct_real", v)
	}
	if v := got.Get("session-id"); v != "sess" {
		t.Errorf("session-id=%q, want sess", v)
	}
	if v := got.Get("originator"); v != "pi" {
		t.Errorf("originator=%q, want pi", v)
	}
	if v := got.Get("Authorization"); v == "" || v == "Bearer spoof" {
		t.Errorf("Authorization=%q, want the Codex bearer token", v)
	}
	for name, want := range map[string]string{
		"OpenAI-Beta":      "responses=experimental",
		"Accept":           "text/event-stream",
		"Content-Type":     "application/json",
		"Content-Encoding": "zstd",
	} {
		if value := got.Get(name); value != want {
			t.Errorf("%s=%q, want %q", name, value, want)
		}
	}
}

func TestCodexSSEPreservesExplicitAffinityWithoutSessionOption(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(reviewSSEDone))
	}))
	defer server.Close()
	stream, err := reviewCodexProvider(t, server.URL, "acct_explicit_affinity").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{
		Transport: TransportSSE,
		Headers: ProviderHeaders{
			"session-id":          new("explicit-session"),
			"x-client-request-id": new("explicit-request"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("stop=%q error=%q", result.StopReason, result.ErrorMessage)
	}
	if value := got.Get("session-id"); value != "explicit-session" {
		t.Errorf("session-id=%q, want explicit-session", value)
	}
	if value := got.Get("x-client-request-id"); value != "explicit-request" {
		t.Errorf("x-client-request-id=%q, want explicit-request", value)
	}
}

// Upstream openai-codex-responses.ts:386-392 sends a zstd-encoded SSE
// request body when the runtime provides zstd compression.
func TestCodexSSEBodyIsZstdCompressed(t *testing.T) {
	var encoding string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoding = r.Header.Get("Content-Encoding")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(reviewSSEDone))
	}))
	defer server.Close()
	stream, err := reviewCodexProvider(t, server.URL, "acct_zstd").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{Transport: TransportSSE})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Result()
	if encoding != "zstd" {
		t.Errorf("Content-Encoding=%q, want zstd", encoding)
	}
}

// Upstream openai-codex-responses.ts:1325-1337 (parseWebSocket onAbort) and
// :1030-1034 (connectWebSocket onAbort) end a request promptly when cancellation
// happens before the first WebSocket event.
func TestCodexAbortWhileWaitingForFirstWebSocketEvent(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	hold := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(reviewSSEDone))
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _, _ = conn.ReadMessage()
		<-hold // never send the first event
	}))
	defer server.Close()
	defer close(hold)
	defer CloseOpenAICodexWebSocketSessions()
	ResetOpenAICodexWebSocketDebugStats()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	done := make(chan *AssistantMessage, 1)
	go func() {
		stream, err := reviewCodexProvider(t, server.URL, "acct_abort").Stream(ctx, reviewCodexCtx("hi"), StreamOptions{Transport: TransportAuto, SessionID: "review-abort"})
		if err != nil {
			done <- &AssistantMessage{StopReason: StopReasonAborted, ErrorMessage: err.Error()}
			return
		}
		done <- stream.Result()
	}()
	select {
	case r := <-done:
		if r.StopReason != StopReasonAborted {
			t.Errorf("stop=%q err=%q, want aborted", r.StopReason, r.ErrorMessage)
		}
	case <-time.After(time.Second):
		t.Fatal("Stream did not observe cancellation while waiting for the first WebSocket event")
	}
	if stats := GetOpenAICodexWebSocketDebugStats("review-abort"); stats != nil && stats.WebSocketFallbackActive {
		t.Errorf("stats=%+v: an abort must not activate sticky SSE fallback (upstream :350-356 rethrows aborted before recordWebSocketFailure)", stats)
	}
}

// Upstream openai-codex-responses.ts:350-356 rethrows cancellation before
// recording a WebSocket transport failure.
func TestCodexAbortDuringHandshakeDoesNotActivateSSEFallback(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(reviewSSEDone))
	}))
	defer server.Close()
	defer close(release)
	defer CloseOpenAICodexWebSocketSessions()
	ResetOpenAICodexWebSocketDebugStats()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		stream, err := reviewCodexProvider(t, server.URL, "acct_abort_dial").Stream(ctx, reviewCodexCtx("hi"), StreamOptions{Transport: TransportAuto, SessionID: "review-abort-dial"})
		if err == nil {
			_ = stream.Result()
		}
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("Stream did not observe cancellation during the WebSocket handshake (gorilla DialContext waits for HandshakeTimeout=15s)")
	}
	if stats := GetOpenAICodexWebSocketDebugStats("review-abort-dial"); stats != nil && (stats.WebSocketFallbackActive || stats.WebSocketFailures != 0) {
		t.Errorf("stats=%+v: abort during handshake must not record a WebSocket failure / sticky SSE fallback", stats)
	}
}

// Upstream openai-codex-responses.ts:507-509 (streamSimple forwards
// options.toolChoice) and :551 (tool_choice: options?.toolChoice ?? "auto").
// At 821305ac ai/openai_responses.go:788 hardcodes "auto".
func TestCodexRequestHonorsToolChoice(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoded, _ := io.ReadAll(r.Body)
		decoded, err := decodeZstdRawFrameForTest(encoded)
		if err != nil {
			t.Error(err)
		} else if err := json.Unmarshal(decoded, &body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(reviewSSEDone))
	}))
	defer server.Close()
	stream, err := reviewCodexProvider(t, server.URL, "acct_tc").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{Transport: TransportSSE, ToolChoice: "none"})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Result()
	if body["tool_choice"] != "none" {
		t.Errorf("tool_choice=%v, want none", body["tool_choice"])
	}
}
