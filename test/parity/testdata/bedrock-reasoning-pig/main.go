package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func encode(events [][2]json.RawMessage) ([]byte, error) {
	var out bytes.Buffer
	for _, event := range events {
		var kind string
		if err := json.Unmarshal(event[0], &kind); err != nil {
			return nil, err
		}
		var headers eventstream.Headers
		headers.Set(":message-type", eventstream.StringValue("event"))
		headers.Set(":event-type", eventstream.StringValue(kind))
		headers.Set(":content-type", eventstream.StringValue("application/json"))
		if err := eventstream.NewEncoder().Encode(&out, eventstream.Message{Headers: headers, Payload: event[1]}); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

func run() error {
	raw, err := os.ReadFile("test/parity/testdata/bedrock-redacted-events.json")
	if err != nil {
		return err
	}
	var events [][2]json.RawMessage
	if err := json.Unmarshal(raw, &events); err != nil {
		return err
	}
	firstResponse, err := encode(events)
	if err != nil {
		return err
	}
	secondResponse, err := encode([][2]json.RawMessage{{json.RawMessage(`"messageStart"`), json.RawMessage(`{"role":"assistant"}`)}, {json.RawMessage(`"messageStop"`), json.RawMessage(`{"stopReason":"guardrail_intervened"}`)}})
	if err != nil {
		return err
	}
	requests := make(chan map[string]json.RawMessage, 2)
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- payload
		count++
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		if count == 1 {
			_, _ = w.Write(firstResponse)
		} else {
			_, _ = w.Write(secondResponse)
		}
	}))
	defer server.Close()
	provider := ai.NewBedrockProvider("global.openai.gpt-5.6-terra", server.URL)
	defer func() { _ = provider.Close() }()
	opts := ai.StreamOptions{CacheRetention: ai.CacheRetentionNone, Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}}
	user := ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{user}}), opts)
	if err != nil {
		return err
	}
	first := stream.Result()
	if len(first.Content) != 1 {
		return fmt.Errorf("first content = %#v", first.Content)
	}
	thinking, ok := first.Content[0].(ai.ThinkingContent)
	if !ok {
		return fmt.Errorf("expected thinking, got %T", first.Content[0])
	}
	fmt.Printf("first=%s thinking=%t:%s text=%s\n", first.StopReason, thinking.Redacted, thinking.ThinkingSignature, thinking.Thinking)
	stream, err = provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{user, *first, ai.UserMessage{Content: ai.UserText("continue"), Timestamp: 2}}}), opts)
	if err != nil {
		return err
	}
	second := stream.Result()
	<-requests
	payload := <-requests
	var messages []struct {
		Role    string `json:"role"`
		Content []struct {
			ReasoningContent *struct {
				RedactedContent string `json:"redactedContent"`
			} `json:"reasoningContent"`
		} `json:"content"`
	}
	if err := json.Unmarshal(payload["messages"], &messages); err != nil {
		return err
	}
	for _, message := range messages {
		if message.Role == "assistant" {
			if len(message.Content) == 0 || message.Content[0].ReasoningContent == nil {
				return fmt.Errorf("missing replay")
			}
			fmt.Printf("replay=%s\n", message.Content[0].ReasoningContent.RedactedContent)
		}
	}
	fmt.Printf("second=%s raw=%s error=%s\n", second.StopReason, second.RawStopReason, second.ErrorMessage)
	return nil
}
