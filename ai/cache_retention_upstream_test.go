package ai

import (
	"reflect"
	"testing"
)

func cacheRetentionTranscript() TranscriptContext {
	return NormalizeContext(Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserText("Hello")}}})
}

func TestCacheRetentionAnthropicUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, env, retention, baseURL string
		compat                        *AnthropicMessagesCompat
		user                          bool
		want                          map[string]any
	}{
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:53
		{name: "should use default cache TTL (no ttl field) when PI_CACHE_RETENTION is not set", want: map[string]any{"type": "ephemeral"}},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:77
		{name: "should use 1h cache TTL when PI_CACHE_RETENTION=long", env: "long", want: map[string]any{"type": "ephemeral", "ttl": "1h"}},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:99
		{name: "should add ttl for non-api.anthropic.com baseUrl by default", env: "long", baseURL: "https://my-proxy.example.com/v1", want: map[string]any{"type": "ephemeral", "ttl": "1h"}},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:138
		{name: "should omit ttl when supportsLongCacheRetention is false", retention: "long", baseURL: "https://my-proxy.example.com/v1", compat: &AnthropicMessagesCompat{SupportsLongCacheRetention: new(false)}, want: map[string]any{"type": "ephemeral"}},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:167
		{name: "should omit cache_control when cacheRetention is none", retention: "none"},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:191
		{name: "should add cache_control to string user messages", user: true, want: map[string]any{"type": "ephemeral"}},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:217
		{name: "should set 1h cache TTL when cacheRetention is long", retention: "long", want: map[string]any{"type": "ephemeral", "ttl": "1h"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_CACHE_RETENTION", tc.env)
			provider := NewAnthropicProvider(AnthropicConfig{APIKey: "fake-key", ProviderID: "anthropic", Model: "claude-haiku-4-5", BaseURL: tc.baseURL, Compat: tc.compat})
			payload := captureSamplingPayload(t, provider, cacheRetentionTranscript(), StreamOptions{CacheRetention: CacheRetention(tc.retention)})
			block := payload["system"].([]any)[0].(map[string]any)
			if tc.user {
				messages := payload["messages"].([]any)
				blocks := messages[len(messages)-1].(map[string]any)["content"].([]any)
				block = blocks[len(blocks)-1].(map[string]any)
			}
			value, exists := block["cache_control"]
			if tc.want == nil {
				if exists {
					t.Fatalf("cache_control must be omitted: %#v", value)
				}
			} else if !reflect.DeepEqual(value, tc.want) {
				t.Fatalf("cache_control = %#v, want %#v", value, tc.want)
			}
		})
	}
}

func TestCacheRetentionResponsesUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:243 (all six rows)
	for _, id := range []string{"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-6-astra", "gpt-6-luna", "gpt-6-sol"} {
		t.Run("does not enable cache warming from the documented TTL alone for "+id, func(t *testing.T) {
			model, ok := LookupModelExact("openai/" + id)
			if !ok {
				t.Fatal("missing model", id)
			}
			if model.PromptCache != nil {
				t.Fatalf("promptCache must be absent: %#v", model.PromptCache)
			}
		})
	}
	for _, tc := range []struct {
		name, model, env, retention, session, baseURL string
		compat                                        *OpenAIResponsesCompat
		want                                          map[string]any
	}{
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:250
		{name: "should not set prompt_cache_retention when PI_CACHE_RETENTION is not set", model: "gpt-4o-mini"},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:272
		{name: "should set prompt_cache_retention to 24h when PI_CACHE_RETENTION=long", model: "gpt-4o-mini", env: "long", want: map[string]any{"prompt_cache_retention": "24h"}},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:295
		{name: "should set prompt_cache_retention for non-api.openai.com baseUrl by default", model: "gpt-4o-mini", env: "long", baseURL: "https://my-proxy.example.com/v1", want: map[string]any{"prompt_cache_retention": "24h"}},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:327
		{name: "should omit prompt_cache_retention when supportsLongCacheRetention is false", model: "gpt-4o-mini", retention: "long", session: "session-compat-false", compat: &OpenAIResponsesCompat{SupportsLongCacheRetention: new(false)}, want: map[string]any{"prompt_cache_key": "session-compat-false"}},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:355
		{name: "should omit prompt_cache_key and disable implicit writes when cacheRetention is none", model: "gpt-5.6-sol", retention: "none", session: "session-1", want: map[string]any{"prompt_cache_options": map[string]any{"mode": "explicit"}}},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:382
		{name: "should omit prompt_cache_options for models that reject it", model: "gpt-4o-mini", retention: "none", session: "session-1"},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:408 (all four rows)
		{name: "should use the supported long cache field for gpt-4o-mini", model: "gpt-4o-mini", retention: "long", session: "session-2", want: map[string]any{"prompt_cache_key": "session-2", "prompt_cache_retention": "24h"}},
		{name: "should use the supported long cache field for gpt-6-astra", model: "gpt-6-astra", retention: "long", session: "session-2", want: map[string]any{"prompt_cache_key": "session-2", "prompt_cache_options": map[string]any{"ttl": "30m"}}},
		{name: "should use the supported long cache field for gpt-6-sol", model: "gpt-6-sol", retention: "long", session: "session-2", want: map[string]any{"prompt_cache_key": "session-2", "prompt_cache_options": map[string]any{"ttl": "30m"}}},
		{name: "should use the supported long cache field for gpt-6-luna", model: "gpt-6-luna", retention: "long", session: "session-2", want: map[string]any{"prompt_cache_key": "session-2", "prompt_cache_options": map[string]any{"ttl": "30m"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_CACHE_RETENTION", tc.env)
			metadata, ok := LookupModelExact("openai/" + tc.model)
			if !ok {
				t.Fatal("missing model", tc.model)
			}
			compat := metadata.Compat
			if tc.compat != nil {
				compat = tc.compat
			}
			provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "fake-key", ProviderID: "openai", Model: tc.model, BaseURL: tc.baseURL, Compat: compat})
			payload := captureSamplingPayload(t, provider, cacheRetentionTranscript(), StreamOptions{CacheRetention: CacheRetention(tc.retention), SessionID: tc.session})
			assertCacheFields(t, payload, tc.want)
		})
	}
}

func assertCacheFields(t *testing.T, payload, want map[string]any) {
	t.Helper()
	for _, key := range []string{"prompt_cache_key", "prompt_cache_retention", "prompt_cache_options"} {
		value, present := payload[key]
		expected, shouldBePresent := want[key]
		if present != shouldBePresent || !reflect.DeepEqual(value, expected) {
			t.Errorf("%s = %#v (present %v), want %#v (present %v)", key, value, present, expected, shouldBePresent)
		}
	}
}

func TestCacheRetentionCompletionsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, session string
		compat        *OpenAICompat
		want          map[string]any
	}{
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:457
		{name: "should set prompt_cache_retention for non-api.openai.com baseUrl by default", session: "session-completions", want: map[string]any{"prompt_cache_key": "session-completions", "prompt_cache_retention": "24h"}},
		// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:482
		{name: "should omit prompt_cache_retention when supportsLongCacheRetention is false", session: "session-completions-false", compat: &OpenAICompat{SupportsLongCacheRetention: new(false)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_CACHE_RETENTION", "")
			provider := NewOpenAIProvider(OpenAIConfig{APIKey: "fake-key", ProviderID: "test-openai-completions", Model: "test-model", BaseURL: "https://my-proxy.example.com/v1", Compat: tc.compat})
			payload := captureSamplingPayload(t, provider, cacheRetentionTranscript(), StreamOptions{CacheRetention: "long", SessionID: tc.session})
			assertCacheFields(t, payload, tc.want)
		})
	}
	// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:507 (all six rows)
	for _, spec := range []string{"opencode/deepseek-v4-flash", "opencode/deepseek-v4-pro", "opencode/kimi-k2.5", "opencode/kimi-k2.6", "opencode/minimax-m2.7", "opencode-go/kimi-k2.6"} {
		t.Run("should omit long cache retention for "+spec, func(t *testing.T) {
			model, ok := LookupModelExact(spec)
			if !ok {
				t.Fatal("missing model", spec)
			}
			if model.Compat == nil || model.Compat.SupportsLongCacheRetention == nil || *model.Compat.SupportsLongCacheRetention {
				t.Fatalf("supportsLongCacheRetention must be false: %#v", model.Compat)
			}
			provider := NewOpenAIProvider(OpenAIConfig{APIKey: "fake-key", ProviderID: model.Provider, Model: model.ID, BaseURL: model.BaseURL, Compat: model.Compat})
			payload := captureSamplingPayload(t, provider, cacheRetentionTranscript(), StreamOptions{CacheRetention: "long", SessionID: "session-opencode-long-cache-unsupported"})
			assertCacheFields(t, payload, nil)
		})
	}
	// .upstream/v0.87.1/packages/ai/test/cache-retention.test.ts:541 (both rows)
	for _, id := range []string{"gpt-oss-120b", "qwen-3.8-27b"} {
		t.Run("should omit strict field on tools for cerebras/"+id, func(t *testing.T) {
			model, ok := LookupModelExact("cerebras/" + id)
			if !ok {
				t.Fatal("missing model", id)
			}
			if model.Compat != nil && model.Compat.SupportsStrictMode != nil {
				t.Fatal("supportsStrictMode must be absent")
			}
			tools := []ToolSchema{
				{Name: "t1", Description: "strict tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}, "required": []string{"x"}}, ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema"}},
				{Name: "t2", Description: "non-strict tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{"y": map[string]any{"type": "string"}}, "required": []string{"y"}}},
			}
			transcript := NormalizeContext(Context{SystemPrompt: "test", Tools: tools, Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}})
			provider := NewOpenAIProvider(OpenAIConfig{APIKey: "fake-key", ProviderID: model.Provider, Model: model.ID, BaseURL: model.BaseURL, Compat: model.Compat})
			payload := captureSamplingPayload(t, provider, transcript, StreamOptions{SessionID: "test"})
			for _, tool := range payload["tools"].([]any) {
				if value, exists := tool.(map[string]any)["function"].(map[string]any)["strict"]; exists {
					t.Fatalf("strict must be omitted: %#v", value)
				}
			}
		})
	}
}
