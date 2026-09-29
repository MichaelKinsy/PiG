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
	raw, err := os.ReadFile("test/parity/testdata/bedrock-cache-cases.json")
	if err != nil {
		return err
	}
	var cases []struct{ Name, Model, Option, Env, Force, AmbientForce string }
	if err := json.Unmarshal(raw, &cases); err != nil {
		return err
	}
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- request
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	}))
	defer server.Close()
	for _, tc := range cases {
		if err := os.Setenv("AWS_BEDROCK_FORCE_CACHE", tc.AmbientForce); err != nil {
			return err
		}
		provider := ai.NewBedrockProvider(tc.Model, server.URL)
		defer func() { _ = provider.Close() }()
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{SystemPrompt: "system", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}}}), ai.StreamOptions{CacheRetention: ai.CacheRetention(tc.Option), Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1", "PI_CACHE_RETENTION": tc.Env, "AWS_BEDROCK_FORCE_CACHE": tc.Force}})
		if err != nil {
			return err
		}
		stream.Result()
		var payload map[string]any
		select {
		case payload = <-requests:
		default:
			return fmt.Errorf("%s: provider settled without sending the request", tc.Name)
		}
		if err := json.NewEncoder(os.Stdout).Encode([]any{tc.Name, payload["system"], payload["messages"]}); err != nil {
			return err
		}
	}
	return nil
}
