package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func captureCompletionsCacheRequest(t *testing.T, cfg OpenAIConfig, options StreamOptions) (map[string]json.RawMessage, http.Header) {
	t.Helper()
	provider := NewOpenAIProvider(cfg).(*openAIProvider)
	var payload map[string]json.RawMessage
	var headers http.Header
	provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		headers = r.Header.Clone()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cached_tokens\":0},\"completion_tokens_details\":{\"reasoning_tokens\":0}}}\n\n"))}, nil
	})}
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "sys", Messages: []Message{UserMessage{Content: UserText("hi")}}}), options)
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatal(result)
	}
	return payload, headers
}

func BenchmarkOpenAICompletionsAffinityRequest(b *testing.B) {
	provider := NewOpenAIProvider(OpenAIConfig{Model: "gpt-4o-mini", ProviderID: "openai", APIKey: "test", BaseURL: "https://proxy.example.com/v1", Compat: &OpenAICompat{SendSessionAffinityHeaders: new(true)}}).(*openAIProvider)
	provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("x-session-affinity") != "session-benchmark" {
			b.Fatal("missing affinity header")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))}, nil
	})}
	transcript := NormalizeContext(Context{SystemPrompt: "You are helpful.", Messages: []Message{UserMessage{Content: UserText("Explain the next step.")}}})
	options := StreamOptions{SessionID: "session-benchmark", CacheRetention: CacheRetentionLong}
	b.ReportAllocs()
	for b.Loop() {
		stream, err := provider.Stream(b.Context(), transcript, options)
		if err != nil {
			b.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != StopReasonStop {
			b.Fatal(result)
		}
	}
}

func TestOpenAICompletionsPromptCacheUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, provider, model, url, catalog, session, env string
		compat                                            *OpenAICompat
		retention                                         CacheRetention
		override                                          ProviderHeaders
		key, ret                                          *string
		headers                                           map[string]string
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:115
		{name: "sets prompt_cache_key for direct OpenAI requests when caching is enabled", session: "session-123", key: new("session-123")},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:122
		{name: "sets prompt_cache_retention to 24h for direct OpenAI requests when cacheRetention is long", session: "session-456", retention: CacheRetentionLong, key: new("session-456"), ret: new("24h")},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:129
		{name: "clamps prompt_cache_key to OpenAI's 64-character limit", session: strings.Repeat("x", 67), key: new(strings.Repeat("x", 64))},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:136
		{name: "omits prompt cache fields when cacheRetention is none", session: "session-789", retention: CacheRetentionNone},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:143
		{name: "omits prompt cache fields for non-OpenAI base URLs without compatible long retention", session: "session-proxy", url: "https://proxy.example.com/v1", compat: &OpenAICompat{SupportsLongCacheRetention: new(false)}, retention: CacheRetentionLong},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:154
		{name: "uses PI_CACHE_RETENTION for direct OpenAI requests", session: "session-env", env: "long", key: new("session-env"), ret: new("24h")},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:162
		{name: "sends known session-affinity headers when compat.sendSessionAffinityHeaders is enabled", session: "session-affinity", url: "https://proxy.example.com/v1", compat: &OpenAICompat{SendSessionAffinityHeaders: new(true)}, headers: map[string]string{"session_id": "session-affinity", "x-client-request-id": "session-affinity", "x-session-affinity": "session-affinity"}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:174
		{name: "sends Fireworks session affinity for accounts/fireworks/models/glm-5p2", catalog: "fireworks/accounts/fireworks/models/glm-5p2", session: "fireworks-session", headers: map[string]string{"session_id": "fireworks-session", "x-client-request-id": "fireworks-session", "x-session-affinity": "fireworks-session"}},
		{name: "sends Fireworks session affinity for accounts/fireworks/routers/glm-5p2-fast", catalog: "fireworks/accounts/fireworks/routers/glm-5p2-fast", session: "fireworks-session", headers: map[string]string{"session_id": "fireworks-session", "x-client-request-id": "fireworks-session", "x-session-affinity": "fireworks-session"}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:184
		{name: "sends Baseten session affinity for built-in catalog models", catalog: "baseten/zai-org/GLM-5.2", session: "baseten-catalog-session", headers: map[string]string{"session_id": "baseten-catalog-session", "x-client-request-id": "baseten-catalog-session", "x-session-affinity": "baseten-catalog-session"}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:192
		{name: "uses OpenAI no-session format when configured", session: "session-nosession", compat: &OpenAICompat{SendSessionAffinityHeaders: new(true), SessionAffinityFormat: SessionAffinityOpenAINoSession}, key: new("session-nosession"), headers: map[string]string{"x-client-request-id": "session-nosession", "x-session-affinity": "session-nosession"}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:206
		{name: "uses OpenRouter session-affinity header when configured", url: "https://proxy.example.com/v1", session: "session-proxy", compat: &OpenAICompat{SendSessionAffinityHeaders: new(true), SessionAffinityFormat: SessionAffinityOpenRouter}, headers: map[string]string{"x-session-id": "session-proxy"}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:221
		{name: "sends OpenRouter session-affinity header by default for built-in OpenRouter models", catalog: "openrouter/auto", session: "session-openrouter", headers: map[string]string{"x-session-id": "session-openrouter"}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:233
		{name: "omits OpenRouter session-affinity data when disabled", provider: "openrouter", url: "https://openrouter.ai/api/v1", session: "session-openrouter", compat: &OpenAICompat{SendSessionAffinityHeaders: new(false)}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:246
		{name: "omits session-affinity headers when cacheRetention is none", session: "session-affinity", url: "https://proxy.example.com/v1", retention: CacheRetentionNone, compat: &OpenAICompat{SendSessionAffinityHeaders: new(true)}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-prompt-cache.test.ts:258
		{name: "lets explicit headers override generated session-affinity headers", session: "session-affinity", url: "https://proxy.example.com/v1", compat: &OpenAICompat{SendSessionAffinityHeaders: new(true)}, override: ProviderHeadersFromStrings(map[string]string{"session_id": "override-session", "x-client-request-id": "override-request", "x-session-affinity": "override-affinity"}), headers: map[string]string{"session_id": "override-session", "x-client-request-id": "override-request", "x-session-affinity": "override-affinity"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_CACHE_RETENTION", tc.env)
			cfg := OpenAIConfig{APIKey: "test-key", Model: "gpt-4o-mini", ProviderID: "openai", BaseURL: "https://api.openai.com/v1", Compat: tc.compat}
			if tc.catalog != "" {
				model, ok := LookupModelExact(tc.catalog)
				if !ok {
					t.Fatalf("missing model %s", tc.catalog)
				}
				cfg.Model, cfg.ProviderID, cfg.BaseURL, cfg.Compat = model.ID, model.Provider, model.BaseURL, model.Compat
			}
			if tc.provider != "" {
				cfg.ProviderID = tc.provider
			}
			if tc.model != "" {
				cfg.Model = tc.model
			}
			if tc.url != "" {
				cfg.BaseURL = tc.url
			}
			payload, headers := captureCompletionsCacheRequest(t, cfg, StreamOptions{SessionID: tc.session, CacheRetention: tc.retention, Headers: tc.override})
			for _, field := range []struct {
				name string
				want *string
			}{{"prompt_cache_key", tc.key}, {"prompt_cache_retention", tc.ret}, {"session_id", nil}} {
				value, ok := payload[field.name]
				if field.want == nil {
					if ok {
						t.Errorf("unexpected %s=%s", field.name, value)
					}
					continue
				}
				var got string
				if err := json.Unmarshal(value, &got); err != nil {
					t.Errorf("%s: %v", field.name, err)
				} else if got != *field.want {
					t.Errorf("%s=%q want=%q", field.name, got, *field.want)
				}
			}
			for _, name := range []string{"session_id", "x-client-request-id", "x-session-affinity", "x-session-id"} {
				if got := headers.Get(name); got != tc.headers[name] {
					t.Errorf("header %s=%q want=%q", name, got, tc.headers[name])
				}
			}
		})
	}
}
