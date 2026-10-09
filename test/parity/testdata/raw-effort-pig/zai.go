package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func zaiRows(services *coding.AgentSessionServices) [][]any {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			panic(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: "glm-5.2", ProviderID: "zai", Compat: &ai.OpenAICompat{ThinkingFormat: "zai", SupportsReasoningEffort: new(true)}})
	defer func() {
		if err := provider.Close(); err != nil {
			panic(err)
		}
	}()
	model := &ai.Model{ID: "glm-5.2", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: ai.APIOpenAICompletions, Reasoning: true}}
	rows := [][]any{}
	for _, effort := range []string{"xhigh", "high", "max"} {
		result := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}}, ai.StreamOptions{ReasoningEffort: effort})
		body := <-requests
		thinking, _ := body["thinking"].(map[string]any)
		rows = append(rows, []any{"zai", effort, thinking["type"], thinking["clear_thinking"], body["reasoning_effort"], result.StopReason})
	}
	return rows
}
