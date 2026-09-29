package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile("test/parity/testdata/bedrock-tool-choice-cases.json")
	if err != nil {
		return err
	}
	var cases []struct {
		Name   string
		Choice any
		Tools  bool
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		return err
	}
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- payload
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	}))
	defer server.Close()
	provider := ai.NewBedrockProvider("us.anthropic.claude-sonnet-4-5-20250929-v1:0", server.URL)
	defer func() { _ = provider.Close() }()
	for _, row := range cases {
		var tools []ai.ToolSchema
		if row.Tools {
			tools = []ai.ToolSchema{{Name: "read", Description: "Read a file", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}}
		}
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Summarize this"), Timestamp: 1}}, Tools: tools}), ai.StreamOptions{ToolChoice: row.Choice, CacheRetention: ai.CacheRetentionNone, Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}})
		if err != nil {
			return err
		}
		stream.Result()
		select {
		case payload := <-requests:
			if err := json.NewEncoder(os.Stdout).Encode([]any{row.Name, payload["toolConfig"]}); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: provider settled without sending the request", row.Name)
		}
	}
	return nil
}
