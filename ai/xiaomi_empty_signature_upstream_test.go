package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// upstream: packages/ai/test/xiaomi-token-plan-ams-anthropic-empty-signature-smoke.test.ts:8-21.
func xiaomiEmptySignatureModel() *Model {
	return &Model{ID: "mimo-v2.5-pro", DisplayName: "MiMo-V2.5-Pro Anthropic smoke", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "xiaomi-token-plan-ams", BaseURL: "https://token-plan-ams.xiaomimimo.com/anthropic", Reasoning: true, Compat: &ModelCompat{AllowEmptySignature: new(true)}}, Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh, ContextWindow: 1048576, MaxOutputTokens: 1024, InputCostPer1M: 1, OutputCostPer1M: 3, CacheReadCostPer1M: 0.2, CacheWriteCostPer1M: 0}}
}

// upstream: packages/ai/test/xiaomi-token-plan-ams-anthropic-empty-signature-smoke.test.ts:35-47.
func xiaomiEmptySignatureContext() Context {
	return Context{SystemPrompt: "You are concise. Follow the requested output format exactly.", Messages: []Message{UserMessage{Content: UserText("Think internally if you need to, then reply with exactly this text and nothing else: first-ok"), Timestamp: time.Now().UnixMilli()}}}
}

// This is request construction evidence, not a replacement for the live empty-signature test.
// upstream: packages/ai/test/xiaomi-token-plan-ams-anthropic-empty-signature-smoke.test.ts:76-81; packages/ai/src/api/anthropic-messages.ts:889-902,1175.
func TestXiaomiEmptySignatureRequestUpstream(t *testing.T) {
	model := xiaomiEmptySignatureModel()
	provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: model.ID, ProviderID: model.ProviderMeta.ProviderID, BaseURL: model.ProviderMeta.BaseURL, Compat: model.ProviderMeta.Compat, ModelMetadata: model})
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	var captured []byte
	captureErr := errors.New("payload captured")
	stream, err := provider.Stream(t.Context(), NormalizeContext(xiaomiEmptySignatureContext()), StreamOptions{MaxTokens: 512, Thinking: ThinkingLevelHigh, OnPayload: func(value any, _ *Model) (any, error) {
		var err error
		captured, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return nil, captureErr
	}})
	if err != nil && !errors.Is(err, captureErr) {
		t.Fatal(err)
	}
	if stream != nil {
		stream.Result()
	}
	if captured == nil {
		t.Fatal("Expected payload capture before request")
	}
	// Captured from pinned Pi completeSimple with the exact model, prompt, 512-token cap and high reasoning; object member order is not significant.
	assertShapeJSON(t, captured, `{"model":"mimo-v2.5-pro","messages":[{"role":"user","content":[{"type":"text","text":"Think internally if you need to, then reply with exactly this text and nothing else: first-ok","cache_control":{"type":"ephemeral"}}]}],"max_tokens":1024,"stream":true,"betas":["interleaved-thinking-2025-05-14"],"system":[{"type":"text","text":"You are concise. Follow the requested output format exactly.","cache_control":{"type":"ephemeral"}}],"thinking":{"type":"enabled","budget_tokens":1024,"display":"summarized"}}`)
}

// An empty signature is distinct from an omitted signature in upstream's ThinkingContent data contract.
// upstream: packages/ai/src/types.ts:370-373; packages/ai/src/api/anthropic-messages.ts:645.
func TestXiaomiThinkingSignatureJSONRoundTripUpstream(t *testing.T) {
	for _, encoded := range []string{
		`{"type":"thinking","thinking":"internal reasoning"}`,
		`{"type":"thinking","thinking":"internal reasoning","thinkingSignature":""}`,
		`{"type":"thinking","thinking":"internal reasoning","thinkingSignature":"signature"}`,
	} {
		t.Run(encoded, func(t *testing.T) {
			var content ThinkingContent
			if err := json.Unmarshal([]byte(encoded), &content); err != nil {
				t.Fatal(err)
			}
			actual, err := json.Marshal(content)
			if err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, actual, encoded)
		})
	}
}

// A controlled provider stream proves the serialization boundary only; it cannot prove that the Xiaomi service emits unsigned thinking.
// upstream: packages/ai/src/api/anthropic-messages.ts:641-648,699-716 always emits thinkingSignature (including empty) and retains content_block_start thinking/signature before appending deltas.
func TestXiaomiThinkingStreamJSONUpstream(t *testing.T) {
	for _, tc := range []struct{ name, initial, signature, delta, signatureDelta string }{
		{"empty signature", "", "", "internal reasoning", ""},
		{"initial thinking and signature", "initial reasoning", "signature-prefix", " plus delta", "-suffix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_thinking\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"mimo-v2.5-pro\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":%q,\"signature\":%q}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":%q}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":%q}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", tc.initial, tc.signature, tc.delta, tc.signatureDelta)
			}))
			defer server.Close()
			model := xiaomiEmptySignatureModel()
			provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: model.ID, ProviderID: model.ProviderMeta.ProviderID, BaseURL: server.URL, Compat: model.ProviderMeta.Compat, ModelMetadata: model})
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			stream, err := provider.Stream(t.Context(), NormalizeContext(xiaomiEmptySignatureContext()), StreamOptions{MaxTokens: 512, Thinking: ThinkingLevelHigh})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if result.StopReason != StopReasonStop {
				t.Fatalf("stream failed: %#v", result)
			}
			encoded, err := json.Marshal(result.Content)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal([]map[string]string{{"type": "thinking", "thinking": tc.initial + tc.delta, "thinkingSignature": tc.signature + tc.signatureDelta}})
			if err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, encoded, string(want))
		})
	}
}
