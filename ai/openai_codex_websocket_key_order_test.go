package ai

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// openai-codex-responses.ts:1542 sends `JSON.stringify({ type: "response.create", ...requestBody })`, and a cached continuation is
// `{ ...body, previous_response_id, input: delta }` (:1470-1475). JSON.stringify keeps insertion order, so the frame is "type", then the
// keys of buildRequestBody in their order (:553-564) with the input array in its own position, then previous_response_id last; nested
// objects keep the order they were built in too.
func TestCodexWebSocketFramesKeepPisKeyOrder(t *testing.T) {
	var mu sync.Mutex
	var frames []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			mu.Lock()
			frames = append(frames, string(data))
			index := len(frames)
			mu.Unlock()
			_ = conn.WriteMessage(websocket.TextMessage, []byte(codexWSTerminal("resp_"+string(rune('0'+index)))))
		}
	}))
	t.Cleanup(func() {
		CloseOpenAICodexWebSocketSessions()
		ResetOpenAICodexWebSocketDebugStats()
		server.Close()
	})
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "acc_test"), Model: "gpt-5.1-codex", ProviderID: "openai-codex", BaseURL: server.URL}).(*openAIResponsesProvider)
	options := StreamOptions{Transport: TransportWebSocketCached, SessionID: "s-order"}
	messages := []Message{UserMessage{Content: UserText("Say hello"), Timestamp: 1}}
	first, _ := codexWSResult(t, provider, NormalizeContext(Context{SystemPrompt: "You are a helpful assistant.", Messages: messages}), options)
	messages = append(messages, *first, UserMessage{Content: UserText("Now finish"), Timestamp: 2})
	codexWSResult(t, provider, NormalizeContext(Context{SystemPrompt: "You are a helpful assistant.", Messages: messages}), options)

	mu.Lock()
	defer mu.Unlock()
	const head = `{"type":"response.create","model":"gpt-5.1-codex","store":false,"stream":true,"instructions":"You are a helpful assistant.","input":`
	const tail = `,"text":{"verbosity":"low"},"include":["reasoning.encrypted_content"],"prompt_cache_key":"s-order","tool_choice":"auto","parallel_tool_calls":true`
	want := []string{
		head + `[{"role":"user","content":[{"type":"input_text","text":"Say hello"}]}]` + tail + `}`,
		head + `[{"role":"user","content":[{"type":"input_text","text":"Now finish"}]}]` + tail + `,"previous_response_id":"resp_1"}`,
	}
	if len(frames) != 2 || frames[0] != want[0] || frames[1] != want[1] {
		t.Fatalf("frames:\n%q\nwant:\n%q", frames, want)
	}
}
