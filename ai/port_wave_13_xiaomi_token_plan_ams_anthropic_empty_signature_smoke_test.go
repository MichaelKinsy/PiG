//go:build live

package ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPortWave13XiaomiEmptySignatureSmoke(t *testing.T) {
	// upstream: packages/ai/test/xiaomi-token-plan-ams-anthropic-empty-signature-smoke.test.ts:74-115
	t.Run("reproduces empty thinking signatures and preserves them for replay", func(t *testing.T) {
		key := liveProviderKey(t, "xiaomi-token-plan-ams")
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		// upstream: packages/ai/test/xiaomi-token-plan-ams-anthropic-empty-signature-smoke.test.ts:8-21 supplies a custom Anthropic model, not the catalog's same-ID API route.
		model := &Model{ID: "mimo-v2.5-pro", DisplayName: "MiMo-V2.5-Pro Anthropic smoke", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "xiaomi-token-plan-ams", BaseURL: "https://token-plan-ams.xiaomimimo.com/anthropic", Reasoning: true, Compat: &ModelCompat{AllowEmptySignature: new(true)}}, Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh, ContextWindow: 1048576, MaxOutputTokens: 1024, InputCostPer1M: 1, OutputCostPer1M: 3, CacheReadCostPer1M: 0.2, CacheWriteCostPer1M: 0}}
		provider := NewAnthropicProvider(AnthropicConfig{APIKey: key, Model: model.ID, ProviderID: model.ProviderMeta.ProviderID, BaseURL: model.ProviderMeta.BaseURL, Compat: model.ProviderMeta.Compat, ModelMetadata: model})
		defer func() {
			if err := provider.Close(); err != nil {
				t.Error(err)
			}
		}()
		request := Context{SystemPrompt: "You are concise. Follow the requested output format exactly.", Messages: []Message{UserMessage{Content: UserText("Think internally if you need to, then reply with exactly this text and nothing else: first-ok"), Timestamp: time.Now().UnixMilli()}}}
		firstStream, err := provider.Stream(ctx, NormalizeContext(request), StreamOptions{MaxTokens: 512, Thinking: ThinkingHigh})
		if err != nil {
			t.Fatal(err)
		}
		first := firstStream.Result()
		if first.StopReason != StopReasonStop {
			t.Fatalf("first stopReason=%s error=%s", first.StopReason, first.ErrorMessage)
		}
		var thinking []ThinkingContent
		unsigned := false
		for _, block := range first.Content {
			if value, ok := block.(ThinkingContent); ok {
				thinking = append(thinking, value)
				unsigned = unsigned || value.ThinkingSignature == ""
			}
		}
		if len(thinking) == 0 || !unsigned {
			t.Fatal("expected thinking blocks with at least one empty signature")
		}
		request.Messages = append(request.Messages, *first, UserMessage{Content: UserText("Reply with exactly this text and nothing else: second-ok"), Timestamp: time.Now().UnixMilli()})
		var captured struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		payloadCaptured := false
		replay, _ := provider.Stream(ctx, NormalizeContext(request), StreamOptions{MaxTokens: 512, Thinking: ThinkingHigh, OnPayload: func(value any, _ *Model) (any, error) {
			data, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(data, &captured); err != nil {
				return nil, err
			}
			payloadCaptured = true
			return nil, errors.New("payload captured")
		}})
		if replay != nil {
			replay.Result()
		}
		if !payloadCaptured {
			t.Fatal("Expected payload capture before request")
		}
		var blocks []map[string]json.RawMessage
		found := false
		for _, message := range captured.Messages {
			if message.Role == "assistant" {
				found = true
				if err := json.Unmarshal(message.Content, &blocks); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		if !found || blocks == nil {
			t.Fatal("expected assistant payload with content array")
		}
		var replayed []map[string]json.RawMessage
		for _, block := range blocks {
			switch string(block["type"]) {
			case `"thinking"`:
				replayed = append(replayed, block)
			case `"text"`:
				var text string
				if err := json.Unmarshal(block["text"], &text); err != nil {
					t.Fatal(err)
				}
				if text == thinking[0].Thinking {
					t.Fatal("unsigned thinking was converted to text")
				}
			}
		}
		actual, err := json.Marshal(replayed)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := json.Marshal([]map[string]string{{"type": "thinking", "thinking": thinking[0].Thinking, "signature": ""}})
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, actual, string(expected))
	})
}
