//go:build live

package ai

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPortWave13AnthropicThinkingDisableE2E(t *testing.T) {
	// upstream: packages/ai/test/anthropic-thinking-disable.test.ts:164
	t.Run("disables thinking for Claude reasoning models", func(t *testing.T) {
		model := cloneGeneratedModel(t, "anthropic/claude-sonnet-4-5").ToModel()
		key := liveProviderKey(t, "anthropic")
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		request := Context{SystemPrompt: "You are a precise assistant. Follow the requested output format exactly.", Messages: []Message{UserMessage{Content: UserText("Before replying, carefully solve 36863 * 5279 internally. Then reply with the word pong repeated exactly 40 times, separated by single spaces. Do not add any other text."), Timestamp: time.Now().UnixMilli()}}}
		stream, err := StreamSimple(ctx, model, NormalizeContext(request), StreamOptions{APIKey: key, Temperature: 0, TemperatureSet: true, MaxTokens: 160})
		if err != nil {
			t.Fatal(err)
		}
		thinkingEvents, thinkingChars := 0, 0
		for event := range stream.Events(ctx) {
			switch value := event.(type) {
			case ThinkingStartEvent, ThinkingEndEvent:
				thinkingEvents++
			case ThinkingDeltaEvent:
				thinkingEvents++
				thinkingChars += len(value.Delta)
			}
		}
		response := stream.Result()
		if response.StopReason != StopReasonStop {
			t.Fatalf("stopReason=%s error=%s", response.StopReason, response.ErrorMessage)
		}
		if thinkingEvents != 0 || thinkingChars != 0 {
			t.Fatalf("thinking events=%d chars=%d; want 0/0", thinkingEvents, thinkingChars)
		}
		var text strings.Builder
		for _, block := range response.Content {
			switch value := block.(type) {
			case ThinkingContent:
				t.Fatal("unexpected thinking block")
			case TextContent:
				text.WriteString(value.Text)
			}
		}
		if count := len(regexp.MustCompile(`(?i)\bpong\b`).FindAllString(strings.TrimSpace(text.String()), -1)); count < 35 {
			t.Fatalf("pongs=%d; want >=35; text=%q", count, text.String())
		}
	})
}
