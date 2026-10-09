package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
)

// .upstream/v0.99.1/packages/ai/test/openai-responses-compat.test.ts:60 keeps the key an OpenAI API key ("sk-" prefix): any other credential sent to api.openai.com is a Sign in with ChatGPT token.
func responsesCompatConfig(t *testing.T, provider, id string) OpenAIResponsesConfig {
	t.Helper()
	model, ok := LookupModelExact(provider + "/" + id)
	if !ok {
		t.Fatalf("missing model %s/%s", provider, id)
	}
	return OpenAIResponsesConfig{Model: id, ProviderID: provider, BaseURL: model.BaseURL, APIKey: "sk-test-key", IsReasoning: model.Reasoning, Compat: model.Compat}
}

func captureResponsesCompat(t *testing.T, cfg OpenAIResponsesConfig, request Context, options StreamOptions, reply string) (map[string]json.RawMessage, http.Header, *AssistantMessage) {
	t.Helper()
	var payload map[string]json.RawMessage
	var headers http.Header
	p := NewOpenAIResponsesProvider(cfg).(*openAIResponsesProvider)
	p.client = &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		headers = r.Header.Clone()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(reply))}, nil
	})}
	stream, err := p.Stream(t.Context(), NormalizeContext(request), options)
	if err != nil {
		t.Fatal(err)
	}
	return payload, headers, stream.Result()
}

func TestOpenAIResponsesCompatHeadersUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, provider, url, catalog, session string
		format                                SessionAffinityFormat
		retention                             CacheRetention
		override                              ProviderHeaders
		key                                   *string
		headers                               map[string]string
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:292
		{name: "sets cache-affinity headers for official OpenAI Responses requests with a sessionId", session: "session-123", key: new("session-123"), headers: map[string]string{"session_id": "session-123", "x-client-request-id": "session-123"}},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:299
		{name: "clamps prompt_cache_key to OpenAI's 64-character limit", session: strings.Repeat("x", 67), key: new(strings.Repeat("x", 64)), headers: map[string]string{"session_id": strings.Repeat("x", 67), "x-client-request-id": strings.Repeat("x", 67)}},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:331
		{name: "sets cache-affinity headers for proxy OpenAI Responses requests with a sessionId", provider: "opencode", url: "https://proxy.example.com/v1", session: "session-123", key: new("session-123"), headers: map[string]string{"session_id": "session-123", "x-client-request-id": "session-123"}},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:343
		{name: "uses OpenRouter session-affinity header when configured", provider: "proxy", url: "https://proxy.example.com/v1", format: SessionAffinityOpenRouter, session: "session-proxy", key: new("session-proxy"), headers: map[string]string{"x-session-id": "session-proxy"}},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:368
		{name: "auto-detects OpenRouter session-affinity header for OpenRouter Responses endpoints", provider: "openrouter", url: "https://openrouter.ai/api/v1", session: "session-openrouter", key: new("session-openrouter"), headers: map[string]string{"x-session-id": "session-openrouter"}},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:392
		{name: "uses OpenAI no-session format when configured", provider: "proxy", url: "https://proxy.example.com/v1", format: SessionAffinityOpenAINoSession, session: "session-proxy", key: new("session-proxy"), headers: map[string]string{"x-client-request-id": "session-proxy"}},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:417
		{name: "uses OpenAI no-session format for OpenCode Responses models", catalog: "opencode", session: "session-opencode", key: new("session-opencode"), headers: map[string]string{"x-client-request-id": "session-opencode"}},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:437
		{name: "can omit OpenAI session_id header while preserving other affinity data", provider: "opencode", url: "https://proxy.example.com/v1", format: SessionAffinityOpenAINoSession, session: "session-123", key: new("session-123"), headers: map[string]string{"x-client-request-id": "session-123"}},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:460
		{name: "lets explicit headers override the default OpenAI cache-affinity headers", session: "session-123", key: new("session-123"), override: ProviderHeadersFromStrings(map[string]string{"session_id": "override-session", "x-client-request-id": "override-request"}), headers: map[string]string{"session_id": "override-session", "x-client-request-id": "override-request"}},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:473
		{name: "omits OpenAI cache-affinity headers when cacheRetention is none", session: "session-123", retention: CacheRetentionNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_CACHE_RETENTION", "")
			cfg := responsesCompatConfig(t, "openai", "gpt-5.4")
			if tc.catalog != "" {
				cfg = responsesCompatConfig(t, tc.catalog, "gpt-5.4")
				if cfg.Compat == nil || cfg.Compat.SessionAffinityFormat != SessionAffinityOpenAINoSession {
					t.Fatal("OpenCode catalog affinity format")
				}
			}
			if tc.provider != "" {
				cfg.ProviderID = tc.provider
			}
			if tc.url != "" {
				cfg.BaseURL = tc.url
			}
			if tc.format != "" {
				cfg.Compat = &OpenAIResponsesCompat{SessionAffinityFormat: tc.format}
			}
			payload, headers, _ := captureResponsesCompat(t, cfg, Context{SystemPrompt: "sys", Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{SessionID: tc.session, CacheRetention: tc.retention, Headers: tc.override}, "data: [DONE]\n\n")
			if _, ok := payload["session_id"]; ok {
				t.Fatal("unexpected session_id body field")
			}
			if tc.key == nil {
				if _, ok := payload["prompt_cache_key"]; ok {
					t.Fatal("unexpected prompt_cache_key")
				}
			} else {
				data, _ := json.Marshal(*tc.key)
				assertShapeJSON(t, payload["prompt_cache_key"], string(data))
			}
			for _, name := range []string{"session_id", "x-client-request-id", "x-session-id"} {
				if got := headers.Get(name); got != tc.headers[name] {
					t.Errorf("header %s=%q want=%q", name, got, tc.headers[name])
				}
			}
		})
	}
}

func TestOpenAIResponsesCompatDefaultsUpstream(t *testing.T) {
	request := Context{SystemPrompt: "sys", Messages: []Message{UserMessage{Content: UserText("hi")}}}
	// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:75
	t.Run("omits reasoning when no reasoning is requested", func(t *testing.T) {
		payload, _, _ := captureResponsesCompat(t, responsesCompatConfig(t, "github-copilot", "gpt-5-mini"), request, StreamOptions{}, "data: [DONE]\n\n")
		if _, ok := payload["reasoning"]; ok {
			t.Fatalf("reasoning=%s", payload["reasoning"])
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:110
	t.Run("forwards required tool choice", func(t *testing.T) {
		ctx := Context{Messages: []Message{UserMessage{Content: UserText("Do not call ping. Respond with text instead.")}}, Tools: []ToolSchema{{Name: "ping", Description: "Ping", Parameters: JsonObject{"type": "object", "properties": JsonObject{"value": JsonObject{"type": "string"}}, "required": []string{"value"}}}}}
		payload, _, _ := captureResponsesCompat(t, responsesCompatConfig(t, "openai", "gpt-5.4"), ctx, StreamOptions{ToolChoice: "required"}, "data: [DONE]\n\n")
		assertShapeJSON(t, payload["tool_choice"], `"required"`)
		var tools []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(payload["tools"], &tools); err != nil {
			t.Fatal(err)
		}
		if len(tools) != 1 || tools[0].Name != "ping" {
			t.Fatalf("tools=%#v", tools)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:157
	t.Run("sets strict mode explicitly for Cloudflare OpenAI Responses tools", func(t *testing.T) {
		cfg := responsesCompatConfig(t, "cloudflare-ai-gateway", "gpt-5.6-sol")
		if cfg.Compat == nil || cfg.Compat.SupportsStrictMode == nil || !*cfg.Compat.SupportsStrictMode {
			t.Fatal("catalog strict mode is not true")
		}
		cfg.BaseURL = "https://example.invalid/v1"
		ctx := Context{Messages: []Message{UserMessage{Content: UserText("Use a tool.")}}, Tools: []ToolSchema{
			{Name: "ordinary", Description: "An ordinary tool", Parameters: JsonObject{"type": "object", "properties": JsonObject{"path": JsonObject{"type": "string"}, "offset": JsonObject{"type": "number"}}, "required": []string{"path"}}},
			{Name: "constrained", Description: "A constrained tool", Parameters: JsonObject{"type": "object", "properties": JsonObject{"value": JsonObject{"type": "string"}}, "required": []string{"value"}}, ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}},
		}}
		payload, _, _ := captureResponsesCompat(t, cfg, ctx, StreamOptions{}, "data: [DONE]\n\n")
		var tools []struct {
			Name   string `json:"name"`
			Strict *bool  `json:"strict"`
		}
		if err := json.Unmarshal(payload["tools"], &tools); err != nil {
			t.Fatal(err)
		}
		if len(tools) != 2 || tools[0].Name != "ordinary" || tools[0].Strict == nil || *tools[0].Strict || tools[1].Name != "constrained" || tools[1].Strict == nil || !*tools[1].Strict {
			t.Fatalf("tools=%s", payload["tools"])
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:208
	for _, id := range []string{"gpt-5.1", "gpt-5.2", "gpt-5.3-codex", "gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-sol", "gpt-6-luna"} {
		t.Run("sends none reasoning effort for OpenAI "+id+" when no reasoning is requested", func(t *testing.T) {
			payload, _, _ := captureResponsesCompat(t, responsesCompatConfig(t, "openai", id), request, StreamOptions{}, "data: [DONE]\n\n")
			var reasoning struct {
				Effort string `json:"effort"`
			}
			if err := json.Unmarshal(payload["reasoning"], &reasoning); err != nil {
				t.Fatal(err)
			}
			if reasoning.Effort != "none" {
				t.Fatalf("reasoning=%s", payload["reasoning"])
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:255
	for _, id := range []string{"gpt-5", "gpt-5-mini", "gpt-5-nano", "gpt-5-pro", "gpt-5.2-pro", "gpt-5.4-pro", "gpt-5.5-pro"} {
		t.Run("omits reasoning effort for OpenAI "+id+" when off is unsupported", func(t *testing.T) {
			payload, _, _ := captureResponsesCompat(t, responsesCompatConfig(t, "openai", id), request, StreamOptions{}, "data: [DONE]\n\n")
			if _, ok := payload["reasoning"]; ok {
				t.Fatalf("reasoning=%s", payload["reasoning"])
			}
		})
	}
	// .upstream/v0.99.1/packages/ai/test/openai-responses-compat.test.ts:481 (GPT-6 models report Fast mode as "fast" even when "priority" is requested, #10034)
	for _, tc := range []struct {
		id, tier, returned string
		multiplier         float64
	}{
		{"gpt-5.4", "priority", "priority", 2},
		{"gpt-5.5", "priority", "priority", 2.5},
		{"gpt-5.5", "flex", "flex", 0.5},
		{"gpt-6-luna", "priority", "fast", 2},
		{"gpt-6-luna", "fast", "fast", 2},
	} {
		t.Run("applies "+tc.id+" cost multiplier for requested "+tc.tier+" and returned "+tc.returned+" service tier", func(t *testing.T) {
			model, _ := LookupModelExact("openai/" + tc.id)
			options := StreamOptions{ModelCost: (&Model{Capabilities: model.ToCapabilities()}).CostRates(), ServiceTier: tc.tier}
			reply := fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"service_tier\":%q,\"usage\":{\"input_tokens\":100000,\"output_tokens\":100000,\"total_tokens\":200000,\"input_tokens_details\":{\"cached_tokens\":0}}}}\n\n", tc.returned)
			_, _, result := captureResponsesCompat(t, responsesCompatConfig(t, "openai", tc.id), request, options, reply)
			// Input and output have equal token scales in the upstream fixture.
			scale := float64(100000) / 1000000
			wantInput, wantOutput := model.InputCostPerMTokens*tc.multiplier*scale, model.OutputCostPerMTokens*tc.multiplier*scale
			wantTotal := (model.InputCostPerMTokens + model.OutputCostPerMTokens) * tc.multiplier * scale
			// toBeCloseTo(x, 12): |diff| < 5e-13
			for name, pair := range map[string][2]float64{"input": {result.Usage.Cost.Input, wantInput}, "output": {result.Usage.Cost.Output, wantOutput}, "total": {result.Usage.Cost.Total, wantTotal}} {
				if math.Abs(pair[0]-pair[1]) >= 5e-13 {
					t.Errorf("cost %s = %v, want %v (result=%#v rates=%#v)", name, pair[0], pair[1], result, options.ModelCost)
				}
			}
		})
	}
	for _, disabled := range []bool{false, true} {
		// .upstream/v0.87.1/packages/ai/test/openai-responses-compat.test.ts:533,565
		name := "sends max_output_tokens by default"
		if disabled {
			name = "omits max_output_tokens when supportsMaxOutputTokens is false"
		}
		t.Run(name, func(t *testing.T) {
			cfg := responsesCompatConfig(t, "openai", "gpt-5.4")
			if disabled {
				cfg.Compat = cloneCompat(cfg.Compat)
				if cfg.Compat == nil {
					cfg.Compat = &OpenAIResponsesCompat{}
				}
				cfg.Compat.SupportsMaxOutputTokens = new(false)
			}
			payload, _, _ := captureResponsesCompat(t, cfg, request, StreamOptions{MaxTokens: 1024}, "data: [DONE]\n\n")
			if disabled {
				if _, ok := payload["max_output_tokens"]; ok {
					t.Fatal("max_output_tokens present")
				}
			} else {
				assertShapeJSON(t, payload["max_output_tokens"], "1024")
			}
		})
	}
}
