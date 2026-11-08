package ai

// Ports packages/ai/test/openai-responses-prompt-cache.test.ts (.upstream/current).

import (
	"encoding/json"
	"testing"
)

const responsesPromptCacheReply = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":0}}}}\n\n"

func captureResponsesPromptCacheKey(t *testing.T, mode PromptCacheKeyMode, retention CacheRetention) (string, bool) {
	t.Helper()
	var compat *OpenAIResponsesCompat
	if mode != "" {
		compat = &OpenAIResponsesCompat{PromptCacheKeyMode: mode}
	}
	cfg := OpenAIResponsesConfig{Model: "test-model", ProviderID: "test-proxy", BaseURL: "https://proxy.example.com/v1", APIKey: "test-key", Compat: compat}
	request := Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}
	payload, _, result := captureResponsesCompat(t, cfg, request, StreamOptions{SessionID: "session-123", CacheRetention: retention}, responsesPromptCacheReply)
	if result.StopReason != StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
	raw, ok := payload["prompt_cache_key"]
	if !ok {
		return "", false
	}
	var key string
	if err := json.Unmarshal(raw, &key); err != nil {
		t.Fatal(err)
	}
	return key, true
}

func TestOpenAIResponsesPromptCacheKeyPolicyUpstream(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      PromptCacheKeyMode
		retention CacheRetention
		want      string
		sent      bool
	}{
		// .upstream/current/packages/ai/test/openai-responses-prompt-cache.test.ts:63
		{name: "preserves the existing default behavior", want: "session-123", sent: true},
		// .upstream/current/packages/ai/test/openai-responses-prompt-cache.test.ts:67
		{name: "emits the key when enabled", mode: PromptCacheKeyEnabled, want: "session-123", sent: true},
		// .upstream/current/packages/ai/test/openai-responses-prompt-cache.test.ts:71
		{name: "omits the key when disabled", mode: PromptCacheKeyDisabled},
		// .upstream/current/packages/ai/test/openai-responses-prompt-cache.test.ts:75
		{name: "lets cacheRetention none override enabled mode", mode: PromptCacheKeyEnabled, retention: CacheRetentionNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, sent := captureResponsesPromptCacheKey(t, tc.mode, tc.retention)
			if sent != tc.sent || key != tc.want {
				t.Fatalf("prompt_cache_key = %q (sent %t), want %q (sent %t)", key, sent, tc.want, tc.sent)
			}
		})
	}
}
