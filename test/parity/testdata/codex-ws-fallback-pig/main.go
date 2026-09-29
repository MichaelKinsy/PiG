package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	var sse atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The provider's connect deadline ends the pending upgrade.
			<-r.Context().Done()
			return
		}
		sse.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_sse\",\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()
	defer ai.CloseOpenAICodexWebSocketSessions()
	provider := ai.NewOpenAICodexResponsesProvider(ai.OpenAICodexResponsesConfig{
		APIKey: "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct_fallback"}}`)) + ".sig",
		Model:  "gpt-5.2", ProviderID: "openai-codex", BaseURL: server.URL,
	})
	for turn := 1; turn <= 2; turn++ {
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{
			Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}},
		}), ai.StreamOptions{Transport: ai.TransportAuto, SessionID: "fallback-probe", WebSocketConnectTimeoutMs: new(10)})
		if err != nil {
			panic(err)
		}
		var events []string
		for event := range stream.Events(context.Background()) {
			events = append(events, string(event.EventType()))
		}
		result := stream.Result()
		stats := ai.GetOpenAICodexWebSocketDebugStats("fallback-probe")
		if stats == nil {
			panic("missing WebSocket debug stats")
		}
		timedOut := strings.Contains(stats.LastWebSocketError, "timeout") || strings.Contains(stats.LastWebSocketError, "deadline exceeded")
		fmt.Printf("turn=%d events=%s stop=%s response=%s failures=%d fallbacks=%d active=%t sse=%d timeout=%t\n", turn, strings.Join(events, ","), result.StopReason, result.ResponseID, stats.WebSocketFailures, stats.SSEFallbacks, stats.WebSocketFallbackActive, sse.Load(), timedOut)
	}
}
