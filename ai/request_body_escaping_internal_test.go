package ai

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

// The text JSON.stringify writes as it is and encoding/json escapes: '<', '>', '&', U+2028 and U+2029.
const stringifyLiteralText = "a <b> & c \u2028 d \u2029 e"

// api/openai-codex-responses.ts:1542 sends the WebSocket request as socket.send(JSON.stringify({ type: "response.create", ...requestBody })).
func TestCodexWebSocketRequestWritesCharactersAsJSONStringifyDoes(t *testing.T) {
	sent := make(chan []byte, 1)
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
		sent <- data
		_ = conn.WriteJSON(map[string]any{"type": "response.done", "response": map[string]any{"id": "resp_1", "status": "completed"}})
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "acct_ws"), Model: "gpt-5.2", ProviderID: "openai-codex", BaseURL: server.URL})
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText(stringifyLiteralText)}}}), StreamOptions{
		Transport: TransportWebSocket, SessionID: "session-escape", WebSocketConnectTimeoutMs: new(1000), TimeoutMs: new(1000),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Result()
	select {
	case body := <-sent:
		if !bytes.Contains(body, []byte(stringifyLiteralText)) {
			t.Fatalf("WebSocket request does not hold %q as JSON.stringify writes it:\n%s", stringifyLiteralText, body)
		}
	default:
		t.Fatal("no WebSocket request was sent")
	}
}

// api/openrouter-images.ts sends its request through the OpenAI SDK's chat.completions.create, which writes JSON.stringify(body).
func TestOpenRouterImagesRequestWritesCharactersAsJSONStringifyDoes(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	model := ImageModel{ID: "m", API: APIImagesOpenRouter, Provider: "openrouter", BaseURL: server.URL, Output: []string{"image"}}
	request := ImagesContext{Input: []ImagesInputContent{TextContent{Text: stringifyLiteralText}}}
	if _, err := OpenRouterImagesAPI().GenerateImages(context.Background(), &model, request, ImagesOptions{APIKey: "sk-escape"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(stringifyLiteralText)) {
		t.Fatalf("request body does not hold %q as JSON.stringify writes it:\n%s", stringifyLiteralText, body)
	}
}

// api/pi-messages.ts:400 posts body: JSON.stringify(payload).
func TestPiMessagesRequestWritesCharactersAsJSONStringifyDoes(t *testing.T) {
	provider := &piMessagesProvider{cfg: PiMessagesConfig{Model: "m", ProviderID: "p"}}
	body, err := provider.payload(NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText(stringifyLiteralText)}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(stringifyLiteralText)) {
		t.Fatalf("request body does not hold %q as JSON.stringify writes it:\n%s", stringifyLiteralText, body)
	}
}

// A before_provider_request replacement is re-encoded to set stream and lift betas; Pi passes { ...nextParams, stream: true } to client.beta.messages.create (anthropic-messages.ts:640-650), which sends JSON.stringify(body).
func TestAnthropicReplacedPayloadWritesCharactersAsJSONStringifyDoes(t *testing.T) {
	for _, payload := range []map[string]any{
		{"model": "m", "messages": []any{map[string]any{"role": "user", "content": stringifyLiteralText}}},
		{"model": "m", "betas": []string{"b"}, "messages": []any{map[string]any{"role": "user", "content": stringifyLiteralText}}},
	} {
		body, _, err := anthropicWireBody(payload)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte(stringifyLiteralText)) {
			t.Fatalf("request body does not hold %q as JSON.stringify writes it:\n%s", stringifyLiteralText, body)
		}
	}
}
