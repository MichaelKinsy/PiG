package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Summarize this"), Timestamp: 1}}, Tools: []ai.ToolSchema{{Name: "read", Description: "Read a file", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}}})
	requests := make(chan map[string]json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- payload
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	p := ai.NewAzureOpenAIResponsesProvider(ai.AzureOpenAIResponsesConfig{Model: "test-deployment", APIKey: "test-key", BaseURL: server.URL + "/openai/v1"})
	defer func() { _ = p.Close() }()
	for _, choice := range []any{"required", "none", map[string]string{"type": "function", "name": "read"}} {
		stream, err := p.Stream(context.Background(), transcript, ai.StreamOptions{ToolChoice: choice})
		if err != nil {
			return err
		}
		result := stream.Result()
		payload := <-requests
		var got any
		if err := json.Unmarshal(payload["tool_choice"], &got); err != nil {
			return err
		}
		if object, ok := got.(map[string]any); ok {
			got = fmt.Sprintf("%s:%s", object["type"], object["name"])
		}
		var tools []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(payload["tools"], &tools); err != nil {
			return err
		}
		if len(tools) == 0 {
			return fmt.Errorf("missing tool definitions")
		}
		fmt.Printf("choice=%v tools=%d:%s stop=%s\n", got, len(tools), tools[0].Name, result.StopReason)
	}
	invalid := ai.NewAzureOpenAIResponsesProvider(ai.AzureOpenAIResponsesConfig{Model: "test-deployment", APIKey: "test-key", BaseURL: "not-a-url"})
	defer func() { _ = invalid.Close() }()
	_, err := invalid.Stream(context.Background(), transcript, ai.StreamOptions{})
	fmt.Printf("invalid-url=%t\n", err != nil && strings.Contains(err.Error(), "Invalid Azure OpenAI base URL: not-a-url"))
	return nil
}
