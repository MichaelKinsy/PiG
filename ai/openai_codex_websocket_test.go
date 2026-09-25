package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOpenAICodexResponses_AutoReusesWebSocketWithInputDelta(t *testing.T) {
	t.Parallel()

	var connections atomic.Int32
	requests := make(chan map[string]any, 2)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != PiUserAgent() {
			t.Errorf("WebSocket User-Agent = %q, want %q", got, PiUserAgent())
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "acct_ws" {
			t.Errorf("WebSocket chatgpt-account-id = %q, want acct_ws", got)
		}
		if got := r.Header.Get("OpenAI-Beta"); got != codexWebSocketBeta {
			t.Errorf("WebSocket OpenAI-Beta = %q, want %q", got, codexWebSocketBeta)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connections.Add(1)
		defer func() { _ = conn.Close() }()
		for requestIndex := 1; ; requestIndex++ {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request map[string]any
			if err := json.Unmarshal(data, &request); err != nil {
				t.Error(err)
				return
			}
			requests <- request
			itemID := "msg_" + string(rune('0'+requestIndex))
			responseID := "resp_" + string(rune('0'+requestIndex))
			for _, event := range []map[string]any{
				{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": itemID, "status": "in_progress"}},
				{"type": "response.output_text.delta", "output_index": 0, "delta": "answer"},
				{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": itemID, "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "answer"}}}},
				{"type": "response.done", "response": map[string]any{"id": responseID, "model": "gpt-5.2", "status": "completed", "usage": map[string]any{"input_tokens": 2, "output_tokens": 1, "total_tokens": 3}}},
			} {
				if err := conn.WriteJSON(event); err != nil {
					return
				}
			}
		}
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()
	ResetOpenAICodexWebSocketDebugStats()

	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
		APIKey:     codexTestToken(t, "acct_ws"),
		Model:      "gpt-5.2",
		ProviderID: "openai-codex",
		BaseURL:    server.URL,
	})
	options := StreamOptions{
		Transport:                 TransportAuto,
		SessionID:                 "session-ws",
		WebSocketConnectTimeoutMs: 1000,
		TimeoutMs:                 1000,
	}
	firstContext := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("one")}}})
	firstStream, err := provider.Stream(context.Background(), firstContext, options)
	if err != nil {
		t.Fatal(err)
	}
	first := firstStream.Result()
	if first.StopReason != StopReasonStop {
		t.Fatalf("first stopReason = %q, error = %q", first.StopReason, first.ErrorMessage)
	}

	secondContext := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("one")},
		*first,
		UserMessage{Content: UserText("two")},
	}})
	secondStream, err := provider.Stream(context.Background(), secondContext, options)
	if err != nil {
		t.Fatal(err)
	}
	second := secondStream.Result()
	if second.StopReason != StopReasonStop {
		t.Fatalf("second stopReason = %q, error = %q", second.StopReason, second.ErrorMessage)
	}

	firstRequest := receiveCodexWebSocketRequest(t, requests)
	secondRequest := receiveCodexWebSocketRequest(t, requests)
	if firstRequest["type"] != "response.create" {
		t.Errorf("first type = %v, want response.create", firstRequest["type"])
	}
	if secondRequest["previous_response_id"] != "resp_1" {
		t.Errorf("second previous_response_id = %v, want resp_1", secondRequest["previous_response_id"])
	}
	secondInput, _ := secondRequest["input"].([]any)
	if len(secondInput) != 1 {
		t.Errorf("second input items = %d, want only the new user delta; payload=%#v", len(secondInput), secondInput)
	}
	if got := connections.Load(); got != 1 {
		t.Errorf("connections = %d, want 1 reused connection", got)
	}
	stats := GetOpenAICodexWebSocketDebugStats("session-ws")
	if stats == nil || stats.ConnectionsCreated != 1 || stats.ConnectionsReused != 1 || stats.DeltaRequests != 1 {
		t.Errorf("debug stats = %+v, want created=1 reused=1 delta=1", stats)
	}
}

func TestAcquireCodexWebSocket_BusyConnectionKeepsCachedOwner(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()

	endpoint := server.URL + "/codex/responses"
	first, err := acquireCodexWebSocket(context.Background(), endpoint, nil, "busy-session", "busy-account", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquireCodexWebSocket(context.Background(), endpoint, nil, "busy-session", "busy-account", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if second.entry != nil {
		t.Fatal("concurrent one-shot connection replaced the busy cached connection")
	}
	second.release(false)
	first.release(true)

	third, err := acquireCodexWebSocket(context.Background(), endpoint, nil, "busy-session", "busy-account", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer third.release(false)
	if !third.reused || third.connection != first.connection {
		t.Error("released cached owner was not reused after a concurrent one-shot connection")
	}
}

func TestOpenAICodexResponses_AutoFallsBackToSSEBeforeOutput(t *testing.T) {
	var websocketAttempts atomic.Int32
	var sseAttempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			websocketAttempts.Add(1)
			time.Sleep(100 * time.Millisecond)
			return
		}
		sseAttempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_sse\",\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()
	ResetOpenAICodexWebSocketDebugStats()

	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
		APIKey:     codexTestToken(t, "acct_fallback"),
		Model:      "gpt-5.2",
		ProviderID: "openai-codex",
		BaseURL:    server.URL,
	})
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{
		Messages: []Message{UserMessage{Content: UserText("hello")}},
	}), StreamOptions{
		Transport:                 TransportAuto,
		SessionID:                 "session-fallback",
		WebSocketConnectTimeoutMs: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonStop || result.ResponseID != "resp_sse" {
		t.Fatalf("result = stop %q response %q error %q", result.StopReason, result.ResponseID, result.ErrorMessage)
	}
	if websocketAttempts.Load() != 1 || sseAttempts.Load() != 1 {
		t.Errorf("attempts = websocket %d SSE %d, want 1 each", websocketAttempts.Load(), sseAttempts.Load())
	}
	stats := GetOpenAICodexWebSocketDebugStats("session-fallback")
	if stats == nil || stats.WebSocketFailures != 1 || stats.SSEFallbacks != 1 || !stats.WebSocketFallbackActive {
		t.Errorf("debug stats = %+v, want one active fallback", stats)
	}
}

func receiveCodexWebSocketRequest(t *testing.T, requests <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Codex WebSocket request")
		return nil
	}
}
