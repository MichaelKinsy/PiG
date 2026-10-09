package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func main() {
	dir, err := os.MkdirTemp("", "google-options-")
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		panic(err)
	}
	captured := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			panic(err)
		}
		cfg, _ := body["generationConfig"].(map[string]any)
		thinking, _ := cfg["thinkingConfig"].(map[string]any)
		captured <- thinking
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer server.Close()
	provider := ai.NewGoogleProvider(ai.GoogleConfig{APIKey: "test", BaseURL: server.URL, ProviderID: "google", Model: "gemini-2.5-pro"})
	defer func() {
		if err := provider.Close(); err != nil {
			panic(err)
		}
	}()
	model := &ai.Model{ID: "gemini-2.5-pro", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: ai.APIGoogleGenerativeAI, Reasoning: true}}
	rows := [][]any{}
	for _, raw := range []string{`{"thinking":{"enabled":true}}`, `{"thinking":{"enabled":true,"budgetTokens":1024}}`, `{"thinking":{"enabled":true,"budgetTokens":0}}`, `{"thinking":{"enabled":true,"budgetTokens":1024,"level":"LOW"}}`, `{"thinking":{"enabled":false}}`} {
		var opts ai.StreamOptions
		if err := json.Unmarshal([]byte(raw), &opts); err != nil {
			panic(err)
		}
		response := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}, opts)
		if response.StopReason != ai.StopReasonStop {
			panic(response.ErrorMessage)
		}
		cfg := <-captured
		rows = append(rows, []any{cfg["includeThoughts"], cfg["thinkingBudget"], cfg["thinkingLevel"]})
	}
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		panic(err)
	}
}
