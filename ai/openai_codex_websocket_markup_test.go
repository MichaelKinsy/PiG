package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// Pi sends the Codex websocket request as socket.send(JSON.stringify({ type: "response.create", ...requestBody })) (openai-codex-responses.ts:1542), so
// markup in a message reaches the socket as written, as it does on the HTTP request bodies (TestProviderRequestBodiesWriteMarkupLiterally).
func TestOpenAICodexWebSocketRequestWritesMarkupLiterally(t *testing.T) {
	frames := make(chan []byte, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		frames <- data
		for _, event := range []map[string]any{
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_1", "status": "in_progress"}},
			{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_1", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "ok"}}}},
			{"type": "response.done", "response": map[string]any{"id": "resp_1", "model": "gpt-5.2", "status": "completed", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
		} {
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()

	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "acct_ws"), Model: "gpt-5.2", ProviderID: "openai-codex", BaseURL: server.URL})
	options := StreamOptions{Transport: TransportWebSocket, SessionID: "session-ws-markup", WebSocketConnectTimeoutMs: new(1000), TimeoutMs: new(1000)}
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("<summary>a & b</summary>")}}}), options)
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("stopReason = %q, error = %q", result.StopReason, result.ErrorMessage)
	}
	frame := string(<-frames)
	if !strings.Contains(frame, "<summary>a & b</summary>") || strings.Contains(frame, `\u003c`) || strings.Contains(frame, `\u0026`) {
		t.Fatalf("websocket request = %s, want the markup written literally as JSON.stringify writes it", frame)
	}
}
