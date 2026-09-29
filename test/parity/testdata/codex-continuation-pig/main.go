package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func frames(id, text string) []string {
	return []string{fmt.Sprintf(`{"type":"response.created","response":{"id":%q}}`, id), `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`, fmt.Sprintf(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":%q}]}}`, text), fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"status":"completed","usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`, id)}
}
func run() error {
	for _, mode := range []string{"websocket", "sse"} {
		if err := probe(mode); err != nil {
			return err
		}
	}
	return nil
}
func probe(mode string) error {
	var mu sync.Mutex
	connections, fetches := 0, 0
	var connectionIds, inputLengths []int
	var previous []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			mu.Lock()
			fetches++
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			for _, frame := range frames("resp_sse", "Hello") {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
			}
			return
		}
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		mu.Lock()
		connections++
		id := connections
		mu.Unlock()
		for {
			var body map[string]any
			if err := connection.ReadJSON(&body); err != nil {
				return
			}
			mu.Lock()
			connectionIds = append(connectionIds, id)
			inputLengths = append(inputLengths, len(body["input"].([]any)))
			prev, _ := body["previous_response_id"].(string)
			previous = append(previous, prev)
			index := len(previous)
			mu.Unlock()
			if index == 2 {
				_ = connection.WriteMessage(websocket.TextMessage, []byte(`{"type":"codex.rate_limits","plan_type":"plus"}`))
				_ = connection.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","error":{"code":"previous_response_not_found","message":"Previous response with id 'resp_1' not found."}}`))
				continue
			}
			if index == 3 && mode == "sse" {
				return
			}
			responseID, text := "resp_2", "Recovered"
			if index == 1 {
				responseID, text = "resp_1", "Hello"
			}
			for _, frame := range frames(responseID, text) {
				if err := connection.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
					return
				}
			}
		}
	}))
	defer server.Close()
	session := "missing-continuation-" + mode
	defer ai.CloseOpenAICodexWebSocketSessions(session)
	defer ai.ResetOpenAICodexWebSocketDebugStats(session)
	token := "aaa." + base64.StdEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc_test"}}`)) + ".bbb"
	provider := ai.NewOpenAICodexResponsesProvider(ai.OpenAICodexResponsesConfig{APIKey: token, Model: "gpt-5.1-codex", ProviderID: "openai-codex", BaseURL: server.URL})
	defer func() { _ = provider.Close() }()
	ctx := ai.NormalizeContext(ai.Context{SystemPrompt: "You are a helpful assistant.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Say hello"), Timestamp: 1}}})
	opts := ai.StreamOptions{Transport: ai.TransportWebSocketCached, SessionID: session}
	firstStream, err := provider.Stream(context.Background(), ctx, opts)
	if err != nil {
		return err
	}
	first := firstStream.Result()
	messages := append(ctx.Messages(), *first, ai.UserMessage{Content: ai.UserText("Now finish"), Timestamp: 2})
	secondStream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: messages}), opts)
	if err != nil {
		return err
	}
	starts, errors := 0, 0
	for event := range secondStream.Events(context.Background()) {
		switch event.EventType() {
		case ai.EventStart:
			starts++
		case ai.EventError:
			errors++
		}
	}
	second := secondStream.Result()
	text := ""
	for _, block := range second.Content {
		if block, ok := block.(ai.TextContent); ok {
			text = block.Text
		}
	}
	stats := ai.GetOpenAICodexWebSocketDebugStats(session)
	mu.Lock()
	defer mu.Unlock()
	return json.NewEncoder(os.Stdout).Encode(struct {
		Mode          string        `json:"mode"`
		Stop          ai.StopReason `json:"stop"`
		Text          string        `json:"text"`
		Starts        int           `json:"starts"`
		Errors        int           `json:"errors"`
		Connections   int           `json:"connections"`
		ConnectionIds []int         `json:"connectionIds"`
		InputLengths  []int         `json:"inputLengths"`
		Previous      []string      `json:"previous"`
		Fetches       int           `json:"fetches"`
		Requests      int           `json:"requests"`
		Created       int           `json:"created"`
		Reused        int           `json:"reused"`
		Full          int           `json:"full"`
		Delta         int           `json:"delta"`
		Failures      int           `json:"failures"`
		Fallbacks     int           `json:"fallbacks"`
	}{mode, second.StopReason, text, starts, errors, connections, connectionIds, inputLengths, previous, fetches, stats.Requests, stats.ConnectionsCreated, stats.ConnectionsReused, stats.FullContextRequests, stats.DeltaRequests, stats.WebSocketFailures, stats.SSEFallbacks})
}
