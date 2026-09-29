package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func fireworksCatalogPayload(t *testing.T, id string, opts StreamOptions) map[string]any {
	t.Helper()
	m := mustGeneratedModel(t, "fireworks", id)
	opts.IsReasoning = m.Reasoning
	// streamSimple maps absent reasoning to thinkingEnabled=false.
	if opts.Thinking == "" {
		opts.Thinking = ThinkingOff
	}
	opts.MaxTokens = m.MaxOutputTokens
	var provider Provider
	switch m.API {
	case APIAnthropicMessages:
		provider = NewAnthropicProvider(AnthropicConfig{APIKey: "test-fireworks-key", Model: m.ID, ProviderID: m.Provider, BaseURL: m.BaseURL, Compat: m.Compat})
	case APIOpenAICompletions:
		provider = NewOpenAIProvider(OpenAIConfig{APIKey: "test-fireworks-key", Model: m.ID, ProviderID: m.Provider, BaseURL: m.BaseURL, Compat: m.Compat})
	default:
		t.Fatalf("unexpected API %s", m.API)
	}
	return captureCatalogPayload(t, provider, opts)
}

func TestFireworksModels(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:22
	t.Run("registers the default Kimi K2.6 model via Anthropic-compatible Messages API", func(t *testing.T) {
		m := mustGeneratedModel(t, "fireworks", "accounts/fireworks/models/kimi-k2p6")
		if m.API != APIAnthropicMessages || m.Provider != "fireworks" || m.BaseURL != "https://api.fireworks.ai/inference" || !m.Reasoning || m.ContextWindow != 262000 || m.MaxOutputTokens != 262000 {
			t.Fatalf("model = %+v", m)
		}
		assertCatalogJSON(t, m.Capabilities, `["text","image"]`)
		assertCatalogJSON(t, (&Model{Capabilities: m.ToCapabilities()}).CostRates(), `{"input":0.95,"output":4,"cacheRead":0.16,"cacheWrite":0}`)
	})
	// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:41
	t.Run("aligns GLM 5.2 Fast with GLM 5.2's OpenAI-compatible config", func(t *testing.T) {
		base := mustGeneratedModel(t, "fireworks", "accounts/fireworks/models/glm-5p2")
		fast := mustGeneratedModel(t, "fireworks", "accounts/fireworks/routers/glm-5p2-fast")
		if base.API != fast.API || base.BaseURL != fast.BaseURL || !reflect.DeepEqual(base.Compat, fast.Compat) || !reflect.DeepEqual(base.ThinkingLevelMap, fast.ThinkingLevelMap) {
			t.Fatalf("base=%+v fast=%+v", base, fast)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:51
	for _, id := range []string{"accounts/fireworks/models/glm-5p2", "accounts/fireworks/routers/glm-5p2-fast"} {
		t.Run("omits unsupported long cache retention for "+id, func(t *testing.T) {
			p := fireworksCatalogPayload(t, id, StreamOptions{CacheRetention: CacheRetentionLong, SessionID: "test-fireworks-session"})
			if _, ok := p["prompt_cache_retention"]; ok {
				t.Fatalf("payload = %v", p)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:76
	t.Run("routes Kimi K3 through the OpenAI-compatible API with native effort controls", func(t *testing.T) {
		for _, id := range []string{"accounts/fireworks/models/kimi-k3", "accounts/fireworks/routers/kimi-k3-fast"} {
			m := mustGeneratedModel(t, "fireworks", id)
			if m.API != APIOpenAICompletions || m.BaseURL != "https://api.fireworks.ai/inference/v1" {
				t.Fatalf("model = %+v", m)
			}
			assertCatalogJSON(t, m.Compat, `{"supportsStore":false,"supportsDeveloperRole":false,"supportsStrictMode":true,"requiresReasoningContentOnAssistantMessages":true,"thinkingFormat":"openai","supportsMidConvoSystemMessages":true,"supportsMidConvoToolAdditions":true,"sendSessionAffinityHeaders":true,"supportsLongCacheRetention":false}`)
			assertCatalogJSON(t, m.ThinkingLevelMap, `{"off":null,"minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":"max"}`)
		}
		p := fireworksCatalogPayload(t, "accounts/fireworks/models/kimi-k3", StreamOptions{Thinking: ThinkingMax})
		if p["reasoning_effort"] != "max" {
			t.Fatalf("payload = %v", p)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:129
	for _, tc := range []struct {
		id     string
		levels []ThinkingLevel
	}{
		{"accounts/fireworks/models/deepseek-v4-flash-0731", []ThinkingLevel{ThinkingOff, ThinkingLow, ThinkingHigh, ThinkingMax}},
		{"accounts/fireworks/models/deepseek-v4-flash-vision-exp", []ThinkingLevel{ThinkingOff, ThinkingLow, ThinkingHigh, ThinkingMax}},
		{"accounts/fireworks/models/deepseek-v4-pro-0813", []ThinkingLevel{ThinkingOff, ThinkingLow, ThinkingHigh, ThinkingMax}},
		{"accounts/fireworks/models/qwen3p8-max", []ThinkingLevel{ThinkingOff, ThinkingLow, ThinkingMedium, ThinkingXHigh}},
		{"accounts/fireworks/models/qwen3p8-2p4t-a95b", []ThinkingLevel{ThinkingOff, ThinkingLow, ThinkingMedium, ThinkingXHigh}},
	} {
		t.Run("sends native Messages effort levels for "+tc.id, func(t *testing.T) {
			m := mustGeneratedModel(t, "fireworks", tc.id)
			if m.API != APIAnthropicMessages || m.Compat == nil || m.Compat.ForceAdaptiveThinking == nil || !*m.Compat.ForceAdaptiveThinking {
				t.Fatalf("model = %+v", m)
			}
			if got := GetSupportedThinkingLevels(m.ToModel()); !reflect.DeepEqual(got, tc.levels) {
				t.Fatalf("levels = %v, want %v", got, tc.levels)
			}
			for _, level := range tc.levels {
				t.Run(string(level), func(t *testing.T) {
					thinking := level
					if level == ThinkingOff {
						thinking = ""
					}
					p := fireworksCatalogPayload(t, tc.id, StreamOptions{Thinking: thinking})
					if level == ThinkingOff {
						assertCatalogJSON(t, p["thinking"], `{"type":"disabled"}`)
						if _, ok := p["output_config"]; ok {
							t.Fatalf("payload = %v", p)
						}
					} else {
						assertCatalogJSON(t, p["thinking"], `{"type":"adaptive","display":"summarized"}`)
						assertCatalogJSON(t, p["output_config"], `{"effort":"`+string(level)+`"}`)
					}
				})
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:161
	for _, tc := range []struct{ id, want string }{
		{"accounts/fireworks/models/glm-5p2", `["off","high","max"]`},
		{"accounts/fireworks/routers/glm-5p2-fast", `["off","high","max"]`},
		{"accounts/fireworks/models/kimi-k3", `["low","high","max"]`},
		{"accounts/fireworks/routers/kimi-k3-fast", `["low","high","max"]`},
	} {
		t.Run("exposes distinct native effort levels for "+tc.id, func(t *testing.T) {
			assertCatalogJSON(t, GetSupportedThinkingLevels(mustGeneratedModel(t, "fireworks", tc.id).ToModel()), tc.want)
		})
	}
	// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:172
	t.Run("keeps toggle-only Messages models without a verified fallback on budget-based thinking", func(t *testing.T) {
		m := mustGeneratedModel(t, "fireworks", "accounts/fireworks/models/kimi-k2p6")
		if m.Compat.ForceAdaptiveThinking != nil {
			t.Fatal("unexpected forceAdaptiveThinking")
		}
		p := fireworksCatalogPayload(t, m.ID, StreamOptions{Thinking: ThinkingHigh})
		assertCatalogJSON(t, p["thinking"], `{"type":"enabled","budget_tokens":16384,"display":"summarized"}`)
		if _, ok := p["output_config"]; ok {
			t.Fatalf("payload = %v", p)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:192
	t.Run("resolves FIREWORKS_API_KEY from the environment", func(t *testing.T) {
		t.Setenv("FIREWORKS_API_KEY", "test-fireworks-key")
		assertCatalogJSON(t, FindEnvKeys("fireworks", nil), `["FIREWORKS_API_KEY"]`)
		if got := GetEnvAPIKey("fireworks", nil); got != "test-fireworks-key" {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:199
	t.Run("sets Fireworks-specific compat for session affinity and unsupported tool fields", func(t *testing.T) {
		c := mustGeneratedModel(t, "fireworks", "accounts/fireworks/models/kimi-k2p6").Compat
		if c == nil || c.SendSessionAffinityHeaders == nil || !*c.SendSessionAffinityHeaders || c.SupportsEagerToolInputStreaming == nil || *c.SupportsEagerToolInputStreaming || c.SupportsCacheControlOnTools == nil || *c.SupportsCacheControlOnTools || c.SupportsLongCacheRetention == nil || *c.SupportsLongCacheRetention || c.AllowEmptySignature == nil || !*c.AllowEmptySignature {
			t.Fatalf("compat = %+v", c)
		}
	})
}

func TestFireworksAnthropicSessionAffinityAndToolCompat(t *testing.T) {
	for _, tc := range []struct {
		name, provider, session string
		retention               CacheRetention
		disableHeaders          bool
		header, want            string
		toolField               string
		toolValue               any
	}{
		// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:346
		{name: "sends x-session-affinity header for Fireworks models", provider: "fireworks", session: "fireworks-session-1", header: "x-session-affinity", want: "fireworks-session-1"},
		// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:356
		{name: "omits x-session-affinity header for native Anthropic models", provider: "anthropic", session: "anthropic-session-1", header: "x-session-affinity"},
		// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:365
		{name: "omits x-session-affinity header when cacheRetention is none", provider: "fireworks", session: "fireworks-session-2", retention: CacheRetentionNone, header: "x-session-affinity"},
		// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:376
		{name: "sends only x-session-id for OpenRouter models", provider: "openrouter", session: "openrouter-session-1", header: "x-session-id", want: "openrouter-session-1"},
		// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:385
		{name: "omits OpenRouter session headers when cacheRetention is none", provider: "openrouter", session: "openrouter-session-2", retention: CacheRetentionNone, header: "x-session-id"},
		// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:395
		{name: "allows OpenRouter session headers to be disabled", provider: "openrouter", session: "openrouter-session-3", disableHeaders: true, header: "x-session-id"},
		// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:404
		{name: "omits cache_control on tools for Fireworks models", provider: "fireworks", toolField: "cache_control"},
		// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:413
		{name: "omits eager_input_streaming on tools for Fireworks models", provider: "fireworks", toolField: "eager_input_streaming"},
		// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:423
		{name: "sends cache_control on tools for native Anthropic models", provider: "anthropic", toolField: "cache_control", toolValue: map[string]any{"type": "ephemeral"}},
		// .upstream/v0.87.1/packages/ai/test/fireworks-models.test.ts:433
		{name: "sends eager_input_streaming on tools for native Anthropic models", provider: "anthropic", toolField: "eager_input_streaming", toolValue: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var headers http.Header
			var payload map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers = r.Header.Clone()
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
			}))
			defer server.Close()
			cfg := AnthropicConfig{APIKey: "test-key", Model: "claude-opus-4-8", ProviderID: tc.provider, BaseURL: server.URL, Compat: &AnthropicMessagesCompat{}}
			if tc.provider == "fireworks" {
				cfg.Model = "accounts/fireworks/models/kimi-k2p6"
				cfg.Compat = &AnthropicMessagesCompat{AllowEmptySignature: new(true), SendSessionAffinityHeaders: new(true), SupportsEagerToolInputStreaming: new(false), SupportsCacheControlOnTools: new(false), SupportsLongCacheRetention: new(false)}
			} else if tc.provider == "openrouter" {
				cfg.Model = "anthropic/claude-opus-4.8"
			}
			if tc.disableHeaders {
				cfg.Compat = &AnthropicMessagesCompat{SendSessionAffinityHeaders: new(false)}
			}
			p := NewAnthropicProvider(cfg)
			defer func() {
				if err := p.Close(); err != nil {
					t.Error(err)
				}
			}()
			retention := tc.retention
			if retention == "" {
				retention = CacheRetentionShort
			}
			stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Use the tool")}}, Tools: []ToolSchema{{Name: "lookup", Description: "Look up a value", Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}}}}}), StreamOptions{SessionID: tc.session, CacheRetention: retention})
			if err != nil {
				t.Fatal(err)
			}
			_ = stream.Result()
			if payload == nil {
				t.Fatal("request not captured")
			}
			if tc.header != "" && headers.Get(tc.header) != tc.want {
				t.Fatalf("%s = %q, want %q", tc.header, headers.Get(tc.header), tc.want)
			}
			if tc.provider == "openrouter" && headers.Get("x-session-affinity") != "" {
				t.Fatal("unexpected OpenRouter x-session-affinity")
			}
			if tc.toolField != "" {
				tools, ok := payload["tools"].([]any)
				if !ok || len(tools) == 0 {
					t.Fatalf("tools = %v", payload["tools"])
				}
				if tc.toolValue != nil {
					// Upstream asserts the last cache breakpoint and the first eager tool.
					if tc.toolField == "cache_control" {
						tools = tools[len(tools)-1:]
					} else {
						tools = tools[:1]
					}
				}
				for _, raw := range tools {
					tool, ok := raw.(map[string]any)
					if !ok {
						t.Fatalf("tool = %v", raw)
					}
					if !reflect.DeepEqual(tool[tc.toolField], tc.toolValue) {
						t.Fatalf("%s = %v, want %v", tc.toolField, tool[tc.toolField], tc.toolValue)
					}
				}
			}
		})
	}
}
