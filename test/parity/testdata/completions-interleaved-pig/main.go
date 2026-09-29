package main

import (
	"bytes"
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
	data, err := os.ReadFile("test/parity/testdata/completions-interleaved.json")
	if err != nil {
		return err
	}
	var cases []struct {
		Name   string
		Chunks []json.RawMessage
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		return err
	}
	for _, test := range cases {
		if err := probe(test.Name, test.Chunks); err != nil {
			return err
		}
	}
	return nil
}
func probe(name string, chunks []json.RawMessage) error {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			var compact bytes.Buffer
			if err := json.Compact(&compact, chunk); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", compact.Bytes())
		}
	}))
	defer server.Close()
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{Model: "gpt-4o-mini", ProviderID: "openai", APIKey: "test", BaseURL: server.URL})
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Use tools.")}}}), ai.StreamOptions{})
	if err != nil {
		return err
	}
	var eventTypes []ai.AssistantEventType
	for event := range stream.Events(context.Background()) {
		eventTypes = append(eventTypes, event.EventType())
	}
	result := stream.Result()
	if result.StopReason != ai.StopReasonToolUse {
		return fmt.Errorf("response: %#v", result)
	}
	var contents []string
	for _, block := range result.Content {
		switch block := block.(type) {
		case ai.TextContent:
			contents = append(contents, "text:"+block.Text)
		case ai.ThinkingContent:
			contents = append(contents, "thinking:"+block.Thinking+":"+block.ThinkingSignature)
		case ai.ToolCall:
			args, err := json.Marshal(block.Arguments)
			if err != nil {
				return err
			}
			contents = append(contents, "toolCall:"+block.ID+":"+block.Name+":"+string(args))
		}
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Case       string                  `json:"case"`
		EventTypes []ai.AssistantEventType `json:"eventTypes"`
		Contents   []string                `json:"contents"`
	}{name, eventTypes, contents})
}
