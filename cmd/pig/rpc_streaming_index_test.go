package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's Anthropic stream keeps the provider's event index on every open block, and RPC serializes the partial as it is (packages/ai/src/api/anthropic-messages.ts:626-750). The wire object that RPC builds for text and thinking blocks must carry that index after their stable fields.
func TestRPCAssistantContentCarriesOpenBlockIndex(t *testing.T) {
	sse := func(event, data string) string { return fmt.Sprintf("event: %s\ndata: %s\n\n", event, data) }
	body := sse("message_start", `{"type":"message_start","message":{"id":"m","model":"probe","usage":{"input_tokens":1,"output_tokens":1}}}`) +
		sse("content_block_start", `{"type":"content_block_start","index":3,"content_block":{"type":"thinking","thinking":"","signature":""}}`) +
		sse("content_block_start", `{"type":"content_block_start","index":4,"content_block":{"type":"text","text":"hi"}}`) +
		sse("content_block_start", `{"type":"content_block_start","index":5,"content_block":{"type":"redacted_thinking","data":"sig"}}`) +
		sse("content_block_stop", `{"type":"content_block_stop","index":3}`) +
		sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`) +
		sse("message_stop", `{"type":"message_stop"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	provider := ai.NewAnthropicProvider(ai.AnthropicConfig{Model: "probe", APIKey: "k", BaseURL: server.URL})
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("go")}}}), ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var wire []string
	for event := range stream.Events(t.Context()) {
		var partial *ai.AssistantMessage
		switch event := event.(type) {
		case ai.ThinkingStartEvent:
			partial = event.Partial
		case ai.TextStartEvent:
			partial = event.Partial
		}
		if partial == nil {
			continue
		}
		content, err := rpcAssistantContent(partial.Content)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(content)
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, string(encoded))
	}
	want := []string{
		`[{"type":"thinking","thinking":"","thinkingSignature":"","index":3}]`,
		`[{"type":"thinking","thinking":"","thinkingSignature":"","index":3},{"type":"text","text":"hi","index":4}]`,
		`[{"type":"thinking","thinking":"","thinkingSignature":"","index":3},{"type":"text","text":"hi","index":4},{"type":"thinking","thinking":"[Reasoning redacted]","thinkingSignature":"sig","redacted":true,"index":5}]`,
	}
	if !slices.Equal(wire, want) {
		t.Fatalf("rpc content:\n got %v\nwant %v", wire, want)
	}
}
