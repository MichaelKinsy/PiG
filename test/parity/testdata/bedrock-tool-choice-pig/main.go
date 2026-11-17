package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/ai/providers/bedrock"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
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
	requests := make(chan *orderedjson.Object, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			var payload *orderedjson.Object
			if payload, err = orderedjson.Parse(body); err == nil {
				requests <- payload
			}
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	}))
	defer server.Close()
	provider := bedrock.NewBedrockProvider("us.anthropic.claude-sonnet-4-5-20250929-v1:0", server.URL)
	defer func() { _ = provider.Close() }()
	for _, row := range cases {
		var tools []ai.ToolSchema
		var tool ai.ToolSchema // decoded from text as an extension's declaration is: a Go map would carry no member order
		if row.Tools {
			if err := json.Unmarshal([]byte(`{"name":"read","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string","description":"File path"},"offset":{"type":"number"}},"required":["path"]}}`), &tool); err != nil {
				return err
			}
			tools = []ai.ToolSchema{tool}
		}
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Summarize this"), Timestamp: 1}}, Tools: tools}), ai.StreamOptions{ToolChoice: row.Choice, CacheRetention: ai.CacheRetentionNone, Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}})
		if err != nil {
			return err
		}
		stream.Result()
		select {
		case payload := <-requests:
			toolConfig, ok := payload.Get("toolConfig")
			if !ok {
				toolConfig = json.RawMessage("null")
			}
			if err := json.NewEncoder(os.Stdout).Encode([]any{row.Name, toolConfig}); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: provider settled without sending the request", row.Name)
		}
	}
	return nil
}
