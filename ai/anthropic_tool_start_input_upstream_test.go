package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Pi anthropic-messages.ts:659-674 seeds toolcall_start arguments from content_block_start.input. At content_block_stop (:743-747), streamed JSON is authoritative instead. Pause the actual HTTP body so neither observation relies on racing a mutable event pointer.
func TestAnthropicToolUseStartInputUpstream(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"omitted", "", `{}`},
		{"null", `,"input":null`, `{}`},
		{"empty", `,"input":{}`, `{}`},
		{"populated", `,"input":{"path":"from-start","nested":{"flag":true}}`, `{"path":"from-start","nested":{"flag":true}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resume := make(chan struct{})
			release := sync.OnceFunc(func() { close(resume) })
			defer release()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call-1\",\"name\":\"read\"%s}}\n\n", tc.input)
				if err := http.NewResponseController(w).Flush(); err != nil {
					t.Error(err)
					return
				}
				select {
				case <-resume:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			t.Cleanup(server.Close)
			provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "claude-sonnet-4-20250514", ProviderID: "anthropic", BaseURL: server.URL})
			t.Cleanup(func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("read")}}}), StreamOptions{MaxTokens: 1024})
			if err != nil {
				t.Fatal(err)
			}
			seen := false
			for event := range stream.Events(t.Context()) {
				if event, ok := event.(ToolCallStartEvent); ok {
					seen = true
					block := event.Partial.Content[event.ContentIndex].(ToolCall)
					encoded, err := json.Marshal(block.Arguments)
					if err != nil {
						t.Fatal(err)
					}
					assertShapeJSON(t, encoded, tc.want)
					release()
				}
			}
			result := stream.Result()
			if !seen || result.StopReason != StopReasonToolUse {
				t.Fatalf("tool start seen=%t; result=%#v", seen, result)
			}
			encoded, err := json.Marshal(result.Content[0].(ToolCall).Arguments)
			if err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, encoded, `{}`)
		})
	}
}
