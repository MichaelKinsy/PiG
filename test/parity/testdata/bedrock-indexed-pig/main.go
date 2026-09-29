package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"

	"github.com/MichaelKinsy/PiG/ai"
)

type fixture struct {
	Name   string
	Events [][2]json.RawMessage
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile("test/parity/testdata/bedrock-indexed-events.json")
	if err != nil {
		return err
	}
	var cases []fixture
	if err := json.Unmarshal(raw, &cases); err != nil {
		return err
	}
	for _, row := range cases {
		if err := probe(row); err != nil {
			return err
		}
	}
	return nil
}

func probe(row fixture) error {
	var wire bytes.Buffer
	encoder := eventstream.NewEncoder()
	for _, pair := range row.Events {
		var kind string
		if err := json.Unmarshal(pair[0], &kind); err != nil {
			return err
		}
		var headers eventstream.Headers
		headers.Set(":message-type", eventstream.StringValue("event"))
		headers.Set(":event-type", eventstream.StringValue(kind))
		headers.Set(":content-type", eventstream.StringValue("application/json"))
		if err := encoder.Encode(&wire, eventstream.Message{Headers: headers, Payload: pair[1]}); err != nil {
			return err
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(wire.Bytes())
	}))
	defer server.Close()
	provider := ai.NewBedrockProvider("global.openai.gpt-5.6-terra", server.URL)
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}}}), ai.StreamOptions{CacheRetention: ai.CacheRetentionNone, Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}})
	if err != nil {
		return err
	}
	var trace []string
	for event := range stream.Events(context.Background()) {
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		var fields struct {
			ContentIndex *int `json:"contentIndex"`
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		entry := string(event.EventType())
		if fields.ContentIndex != nil {
			entry += ":" + strconv.Itoa(*fields.ContentIndex)
		}
		trace = append(trace, entry)
	}
	result := stream.Result()
	raw, err := json.Marshal([]any{row.Name, result.StopReason, result.Content, trace})
	if err != nil {
		return err
	}
	var canonical any
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(canonical)
}
