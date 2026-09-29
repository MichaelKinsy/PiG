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
)

type inputCase struct{ Name, Text string }

func main() {
	for _, key := range []string{"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
	}
	if home := os.Getenv("BEDROCK_PROBE_HOME"); home != "" {
		if err := os.Setenv("HOME", home); err != nil {
			panic(err)
		}
	}
	data, err := os.ReadFile("test/parity/testdata/bedrock-whitespace-cases.json")
	if err != nil {
		panic(err)
	}
	var cases []inputCase
	if err := json.Unmarshal(data, &cases); err != nil {
		panic(err)
	}
	for _, model := range []string{"anthropic.claude-sonnet-4-5-20250929-v1:0", "amazon.nova-lite-v1:0"} {
		for _, tc := range cases {
			messages := probe(model, tc.Text)
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"model": model, "case": tc.Name, "messages": messages}); err != nil {
				panic(err)
			}
		}
	}
}

func probe(model, text string) json.RawMessage {
	captured := make(chan json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			panic(err)
		}
		captured <- body["messages"]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"captured request"}`)
	}))
	defer server.Close()
	provider := ai.NewBedrockProvider(model, server.URL)
	defer func() { _ = provider.Close() }()
	request := ai.Context{Messages: []ai.Message{
		ai.UserMessage{Content: ai.UserText(text)},
		ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: text}}},
		ai.AssistantMessage{API: ai.APIBedrockConverseStream, Provider: "amazon-bedrock", Model: model, StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: text}, ai.ThinkingContent{Thinking: "reason", ThinkingSignature: text}, ai.ToolCall{ID: "call", Name: "tool", Arguments: ai.JsonObject{}},
		}},
		ai.ToolResultMessage{ToolCallID: "call", ToolName: "tool", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}},
	}}
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(request), ai.StreamOptions{CacheRetention: ai.CacheRetentionNone, Env: ai.ProviderEnv{
		"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1", "PI_CACHE_RETENTION": "none", "HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "NO_PROXY": "*",
	}})
	if stream != nil {
		stream.Result()
	}
	select {
	case messages := <-captured:
		return messages
	default:
		panic(fmt.Sprintf("request did not reach HTTP serializer: %v", err))
	}
}
