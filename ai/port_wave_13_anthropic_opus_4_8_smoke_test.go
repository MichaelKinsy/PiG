//go:build live

package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPortWave13AnthropicOpus48Smoke(t *testing.T) {
	// packages/ai/test/anthropic-opus-4-8-smoke.test.ts:25-69
	t.Run("streams Claude Opus 4.8 with reasoning enabled", func(t *testing.T) {
		key := liveProviderKey(t, "anthropic")
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		provider := newAnthropicTestProvider(t, cloneGeneratedModel(t, "anthropic/claude-opus-4-8"), key)
		request := Context{SystemPrompt: "You are a precise assistant. Follow the user's instructions exactly.", Messages: []Message{UserMessage{Content: UserText("Compute 48291 * 7317 and 90844 - 17729, add the results, and determine whether the sum is divisible by 11. Reply with exactly this format and nothing else: sum=<sum>; divisibleBy11=<yes|no>"), Timestamp: time.Now().UnixMilli()}}}
		var captured map[string]json.RawMessage
		stream, err := provider.Stream(ctx, NormalizeContext(request), StreamOptions{Thinking: ThinkingHigh, MaxTokens: 1024, OnPayload: func(value any, _ *Model) (any, error) {
			data, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(data, &captured); err != nil {
				return nil, err
			}
			return value, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		sawThinking := false
		for event := range stream.Events(ctx) {
			switch event.EventType() {
			case EventThinkingStart, EventThinkingDelta, EventThinkingEnd:
				sawThinking = true
			}
		}
		response := stream.Result()
		if response.StopReason != StopReasonStop || response.ErrorMessage != "" {
			t.Fatalf("stopReason=%s error=%s", response.StopReason, response.ErrorMessage)
		}
		// The approved correction retains the pinned implementation as oracle: packages/ai/test/anthropic-opus-4-8-smoke.test.ts:48 omits display; packages/ai/src/api/anthropic-messages.ts:1164-1167 emits display:"summarized".
		// Existing direct Pi 0.87.1 full-SDK probe: 92a8bbf91ed1ac8cd08c53353b75c6a711ae4f61:test/parity/testdata/anthropic-opus-smoke-probe.mjs.
		assertShapeJSON(t, captured["thinking"], `{"type":"adaptive","display":"summarized"}`)
		assertShapeJSON(t, captured["output_config"], `{"effort":"high"}`)
		if !sawThinking {
			t.Fatal("no thinking events")
		}
		var thinking *ThinkingContent
		var text strings.Builder
		for _, block := range response.Content {
			switch value := block.(type) {
			case ThinkingContent:
				if thinking == nil {
					thinking = &value
				}
			case TextContent:
				text.WriteString(value.Text)
			}
		}
		if thinking == nil || thinking.ThinkingSignature == "" {
			t.Fatal("expected nonempty signed thinking")
		}
		if strings.TrimSpace(text.String()) != "sum=353418362; divisibleBy11=yes" {
			t.Fatalf("text=%q", text.String())
		}
	})
}
