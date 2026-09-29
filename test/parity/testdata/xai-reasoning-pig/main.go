package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func main() {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			panic(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_xai_test\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	}))
	defer server.Close()
	dir, err := os.MkdirTemp("", "xai-runtime-")
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}()
	config := fmt.Sprintf(`{"providers":{"xai":{"baseUrl":%q,"apiKey":"test-token"}}}`, server.URL)
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0600); err != nil {
		panic(err)
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		panic(err)
	}
	rows := [][]any{}
	for _, id := range []string{"grok-4.5", "grok-4.7", "grok-4.3"} {
		model, err := coding.BuildModel("xai/"+id, services)
		if err != nil {
			panic(err)
		}
		for _, effort := range []ai.ThinkingLevel{"", ai.ThinkingMedium} {
			s := services.ModelRuntime().Stream(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}}}, ai.StreamOptions{Thinking: effort})
			result := s.Result()
			if result.StopReason != ai.StopReasonStop {
				panic(result.ErrorMessage)
			}
			body := <-requests
			rows = append(rows, []any{id, effort, body["reasoning"], body["include"], result.StopReason})
		}
		if err := model.Provider.Close(); err != nil {
			panic(err)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		panic(err)
	}
}
