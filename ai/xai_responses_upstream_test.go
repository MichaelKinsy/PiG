package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:116
func TestXAIResponsesExcludesRetiredAndRedundantModels(t *testing.T) {
	for _, id := range []string{"grok-3", "grok-3-fast", "grok-4.20-0309-non-reasoning", "grok-4.20-0309-reasoning", "grok-build-0.1", "grok-code-fast-1"} {
		if _, ok := LookupModelExact("xai/" + id); ok {
			t.Errorf("retired model %s in catalog", id)
		}
	}
}

// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:129
func TestXAIResponsesRoutesEveryBuiltInModelThroughResponses(t *testing.T) {
	models := ListModels("xai")
	if len(models) == 0 {
		t.Fatal("empty xAI catalog")
	}
	for _, m := range models {
		if m.API != APIOpenAIResponses {
			t.Errorf("%s api = %s", m.ID, m.API)
		}
	}
	for _, tc := range []struct {
		id   string
		want []ModelThinkingLevel
	}{
		{"grok-4.5", []ModelThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh}},
		{"grok-4.6", []ModelThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh}},
		{"grok-4.7", []ModelThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh}},
		{"grok-4.3", []ModelThinkingLevel{ThinkingOff, ThinkingLow, ThinkingMedium, ThinkingHigh}},
	} {
		if got := GetSupportedThinkingLevels(upstreamThinkingModel(t, "xai/"+tc.id)); !slices.Equal(got, tc.want) {
			t.Errorf("%s levels = %v, want %v", tc.id, got, tc.want)
		}
	}
}

// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:139
func TestXAIResponsesGrok47CapabilitiesAndLongContextPricing(t *testing.T) {
	m := upstreamThinkingModel(t, "xai/grok-4.7")
	if m.ProviderMeta.API != APIOpenAIResponses || !m.ProviderMeta.Reasoning || !slices.Equal(m.Input, []string{"text", "image"}) || m.Capabilities.ContextWindow != 500000 || m.Capabilities.MaxOutputTokens != 500000 {
		t.Fatalf("model = %+v", m)
	}
	want := ModelCost{Input: 2, Output: 6, CacheRead: 0.5, CacheWrite: 0, Tiers: []CostTier{{InputTokensAbove: 200000, InputCostPer1M: 4, OutputCostPer1M: 12, CacheReadCostPer1M: 1, CacheWriteCostPer1M: 0}}}
	if got := m.CostRates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cost = %+v, want %+v", got, want)
	}
}

func TestXAIResponsesRequestUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, id, system string
		opts             StreamOptions
		want             map[string]any
		absent           []string
	}{
		// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:164
		{"uses /responses with bearer auth and xAI-compatible request fields", "grok-4.5", "You are a careful coding assistant.", StreamOptions{SessionID: "pi-session-123", CacheRetention: CacheRetentionLong, Thinking: ThinkingLevelMedium}, map[string]any{"model": "grok-4.5", "store": false, "stream": true, "prompt_cache_key": "pi-session-123", "reasoning": map[string]any{"effort": "medium", "summary": "auto"}, "include": []any{"reasoning.encrypted_content"}}, []string{"prompt_cache_retention"}},
		// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:202
		{"requests encrypted reasoning without an effort override", "grok-4.5", "", StreamOptions{}, map[string]any{"model": "grok-4.5", "store": false, "include": []any{"reasoning.encrypted_content"}}, []string{"reasoning"}},
		// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:217
		{"uses /responses for Grok 4.7 with xhigh effort and encrypted reasoning", "grok-4.7", "You are a careful coding assistant.", StreamOptions{Thinking: ThinkingLevelXHigh}, map[string]any{"model": "grok-4.7", "store": false, "stream": true, "reasoning": map[string]any{"effort": "xhigh", "summary": "auto"}, "include": []any{"reasoning.encrypted_content"}}, nil},
		// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:240
		{"uses /responses for Grok 4.3", "grok-4.3", "", StreamOptions{Thinking: ThinkingLevelLow}, map[string]any{"model": "grok-4.3", "store": false, "reasoning": map[string]any{"effort": "low", "summary": "auto"}, "include": []any{"reasoning.encrypted_content"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := upstreamThinkingModel(t, "xai/"+tc.id)
			captured, headers, url := captureXAIResponses(t, m, tc.system, tc.opts)
			if url != "https://api.x.ai/v1/responses" {
				t.Errorf("url = %s", url)
			}
			if got := headers.Get("Authorization"); got != "Bearer xai-test-token" {
				t.Errorf("authorization = %q", got)
			}
			// D65 changes only product identity; default/override semantics match Pi.
			if got := headers.Get("User-Agent"); got != PiUserAgent() {
				t.Errorf("user-agent = %q", got)
			}
			if tc.opts.SessionID != "" && headers.Get("session_id") != tc.opts.SessionID {
				t.Errorf("session_id = %q", headers.Get("session_id"))
			}
			for key, want := range tc.want {
				if got, ok := captured[key]; !ok || !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %#v, want %#v", key, got, want)
				}
			}
			for _, key := range tc.absent {
				if got, ok := captured[key]; ok {
					t.Errorf("unexpected %s = %#v", key, got)
				}
			}
			if tc.system != "" {
				input, ok := captured["input"].([]any)
				if !ok {
					t.Fatalf("input = %#v", captured["input"])
				}
				found := false
				for _, item := range input {
					if m, ok := item.(map[string]any); ok && m["role"] == "developer" && m["content"] == tc.system {
						found = true
					}
				}
				if !found {
					t.Errorf("developer message absent from input: %#v", input)
				}
			}
		})
	}
}

func TestXAIResponsesUserAgentUpstream(t *testing.T) {
	for _, tc := range []struct {
		name     string
		api      API
		override string
	}{
		// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:261
		{"uses pi's User-Agent by default for Responses requests", APIOpenAIResponses, ""},
		// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:283
		{"lets explicit headers override the default Responses User-Agent", APIOpenAIResponses, "custom-agent"},
		// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:293
		{"uses pi's User-Agent by default for Completions requests", APIOpenAICompletions, ""},
		// .upstream/v0.87.1/packages/ai/test/xai-responses.test.ts:297
		{"lets explicit headers override the default Completions User-Agent", APIOpenAICompletions, "custom-agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := StreamOptions{}
			if tc.override != "" {
				opts.Headers = ProviderHeaders{"User-Agent": new(tc.override)}
			}
			var headers http.Header
			if tc.api == APIOpenAIResponses {
				m := upstreamThinkingModel(t, "xai/grok-4.5")
				if tc.override == "" {
					m.ProviderMeta.ProviderID = "openai"
					m.ProviderMeta.BaseURL = "https://api.openai.com/v1"
				}
				_, headers, _ = captureXAIResponses(t, m, "", opts)
			} else {
				p := &openAIProvider{cfg: OpenAIConfig{BaseURL: "https://api.x.ai/v1", APIKey: "xai-test-token", Model: "grok-custom", ProviderID: "xai"}, client: sseClient(&headers, "data: {\"id\":\"chatcmpl-ua\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null,\"index\":0}]}\n\ndata: {\"id\":\"chatcmpl-ua\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cached_tokens\":0},\"completion_tokens_details\":{\"reasoning_tokens\":0}}}\n\ndata: [DONE]\n\n")}
				s, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}}), opts)
				if err != nil {
					t.Fatal(err)
				}
				if got := s.Result(); got.StopReason != StopReasonStop {
					t.Fatalf("result = %+v", got)
				}
			}
			// D65 preserves the Pi header contract with PiG's approved product identity.
			want := tc.override
			if want == "" {
				want = PiUserAgent()
			}
			if got := headers.Get("User-Agent"); got != want {
				t.Fatalf("user-agent = %q, want %q", got, want)
			}
		})
	}
}

func captureXAIResponses(t *testing.T, m *Model, system string, opts StreamOptions) (map[string]any, http.Header, string) {
	t.Helper()
	var body map[string]any
	var headers http.Header
	var url string
	p, ok := NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: m.ProviderMeta.BaseURL, APIKey: "xai-test-token", Model: m.ID, ProviderID: m.ProviderMeta.ProviderID, Compat: m.ProviderMeta.Compat, IsReasoning: m.ProviderMeta.Reasoning}).(*openAIResponsesProvider)
	if !ok {
		t.Fatal("unexpected provider type")
	}
	p.client = &http.Client{Transport: userAgentTestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		headers = r.Header.Clone()
		url = r.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"sequence_number\":0,\"response\":{\"id\":\"resp_xai_test\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":0}}}}\n\ndata: [DONE]\n\n"))}, nil
	})}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	s, err := p.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: system, Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}}), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result := s.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("result = %+v", result)
	}
	if body == nil {
		t.Fatal("request was not captured")
	}
	return body, headers, url
}
