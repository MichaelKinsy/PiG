package ai

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// uuidv7SequenceOf extracts the 41-bit sequence that uuid.ts writes into bytes 6 through 11.
func uuidv7SequenceOf(t *testing.T, id string) uint64 {
	t.Helper()
	raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil || len(raw) != 16 || !uuidV7Pattern.MatchString(id) {
		t.Fatalf("%q is not a UUIDv7 (%v)", id, err)
	}
	return uint64(raw[6]&0x0f)<<37 | uint64(raw[7])<<29 | uint64(raw[8]&0x3f)<<23 | uint64(raw[9])<<15 | uint64(raw[10])<<7 | uint64(raw[11]>>1)
}

// upstream: packages/ai/src/api/openai-codex-responses.ts: websocketRequestId = codexSessionId || uuidv7()
// Without a session id the WebSocket request id comes from the shared process-wide generator, so its sequence follows the previous shared id.
func TestOpenAICodexWebSocketRequestIDUsesTheSharedUUIDv7Generator(t *testing.T) {
	requestIDs := make(chan string, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestIDs <- r.Header.Get("x-client-request-id")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for _, event := range []map[string]any{
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_1", "status": "in_progress"}},
			{"type": "response.output_text.delta", "output_index": 0, "delta": "answer"},
			{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_1", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "answer"}}}},
			{"type": "response.done", "response": map[string]any{"id": "resp_1", "model": "gpt-5.2", "status": "completed", "usage": map[string]any{"input_tokens": 2, "output_tokens": 1, "total_tokens": 3}}},
		} {
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()

	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
		APIKey:     codexTestToken(t, "acct_ws"),
		Model:      "gpt-5.2",
		ProviderID: "openai-codex",
		BaseURL:    server.URL,
	})
	before, err := UUIDv7(nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("one")}}}), StreamOptions{
		Transport:                 TransportWebSocket,
		WebSocketConnectTimeoutMs: new(1000),
		TimeoutMs:                 new(1000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("stopReason = %q, error = %q", result.StopReason, result.ErrorMessage)
	}
	requestID := <-requestIDs
	if step := uuidv7SequenceOf(t, requestID) - uuidv7SequenceOf(t, before); step == 0 || step > 1<<16 {
		t.Fatalf("request id %q does not follow the shared generator's %q (sequence step %d)", requestID, before, step)
	}
}
