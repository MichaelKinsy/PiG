package ai

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Ports packages/ai/test/openrouter-cost.test.ts and the #9980 cases of openai-completions-provider-stream-event.test.ts and
// anthropic-sse-parsing.test.ts (Pi cae8d69e5, #10286).

func openRouterUsage() *Usage {
	return &Usage{Input: 1, Output: 1, TotalTokens: 2, Cost: UsageCost{Input: 0.25, Output: 0.75, Total: 1}}
}

func decodeOpenRouterAccounting(t *testing.T, raw string) openRouterUsageAccounting {
	t.Helper()
	var accounting openRouterUsageAccounting
	if err := json.Unmarshal([]byte(raw), &accounting); err != nil {
		t.Fatal(err)
	}
	return accounting
}

func TestIsOpenRouterModel(t *testing.T) {
	for _, tc := range []struct {
		provider, baseURL string
		want              bool
	}{
		{"openrouter", "https://example.com", true},
		{"openrouter-work", "https://openrouter.ai/api/v1", true},
		{"custom", "https://example.com/v1", false},
	} {
		if got := isOpenRouterModel(tc.provider, tc.baseURL); got != tc.want {
			t.Errorf("isOpenRouterModel(%q, %q) = %v, want %v", tc.provider, tc.baseURL, got, tc.want)
		}
	}
}

func TestApplyOpenRouterReportedCost(t *testing.T) {
	for _, tc := range []struct {
		name, accounting string
		want             float64
		fromProvider     bool
	}{
		{"uses OpenRouter's reported cost for non-BYOK requests", `{"cost":0,"is_byok":false,"cost_details":{"upstream_inference_cost":0,"upstream_inference_prompt_cost":0,"upstream_inference_completions_cost":0}}`, 0, true},
		{"adds the upstream inference cost for BYOK requests", `{"cost":0.1,"is_byok":true,"cost_details":{"upstream_inference_cost":0.9,"upstream_inference_prompt_cost":0.6,"upstream_inference_completions_cost":0.3}}`, 1, true},
		{"uses upstream cost components when OpenRouter omits their total", `{"cost":0.1,"is_byok":true,"cost_details":{"upstream_inference_cost":null,"upstream_inference_prompt_cost":0.6,"upstream_inference_completions_cost":0.3}}`, 1, true},
		// openrouter-cost.ts keeps the catalog estimate unless the accounting is complete and valid.
		{"null cost keeps the catalog total", `{"cost":null,"is_byok":false}`, 1, false},
		{"negative cost keeps the catalog total", `{"cost":-1,"is_byok":false}`, 1, false},
		{"string cost keeps the catalog total", `{"cost":"0.1","is_byok":false}`, 1, false},
		{"missing is_byok keeps the catalog total", `{"cost":0.1}`, 1, false},
		{"BYOK without details keeps the catalog total", `{"cost":0.1,"is_byok":true,"cost_details":null}`, 1, false},
		{"BYOK with an absent upstream total keeps the catalog total", `{"cost":0.1,"is_byok":true,"cost_details":{"upstream_inference_prompt_cost":0.6,"upstream_inference_completions_cost":0.3}}`, 1, false},
		{"BYOK with invalid components keeps the catalog total", `{"cost":0.1,"is_byok":true,"cost_details":{"upstream_inference_cost":null,"upstream_inference_prompt_cost":-1,"upstream_inference_completions_cost":0.3}}`, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := openRouterUsage()
			usage.Cost.TotalFromProvider = new(false)
			applyOpenRouterReportedCost(usage, decodeOpenRouterAccounting(t, tc.accounting))
			if math.Abs(usage.Cost.Total-tc.want) > 1e-9 {
				t.Fatalf("total = %v, want %v", usage.Cost.Total, tc.want)
			}
			if usage.Cost.TotalFromProvider == nil || *usage.Cost.TotalFromProvider != tc.fromProvider {
				t.Fatalf("totalFromProvider = %v, want %v", usage.Cost.TotalFromProvider, tc.fromProvider)
			}
			if usage.Cost.Input != 0.25 || usage.Cost.Output != 0.75 {
				t.Fatalf("catalog components changed: %+v", usage.Cost)
			}
		})
	}
}

func openRouterFetch(body string) *http.Client {
	return &http.Client{Transport: responsesTestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
}

// pi-mono #9980: OpenRouter bills more or less than the catalog rates imply; the reported cost is authoritative.
func TestOpenAICompletionsUsesOpenRouterReportedCost(t *testing.T) {
	stream := func(provider, baseURL, usage string) *AssistantMessage {
		t.Helper()
		sse := `data: {"id":"chatcmpl-cost","model":"openrouter/auto","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}],"usage":` + usage + "}\n\ndata: [DONE]\n\n"
		p := NewOpenAIProvider(OpenAIConfig{Model: "openrouter/auto", ProviderID: provider, APIKey: "test", BaseURL: baseURL})
		s, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{Fetch: openRouterFetch(sse), ModelCost: ModelCost{Input: 1000000, Output: 1000000}})
		if err != nil {
			t.Fatal(err)
		}
		return s.Result()
	}
	// non-BYOK: the reported total replaces the catalog total and the upstream cost is not added again.
	message := stream("openrouter-work", "https://openrouter.ai/api/v1", `{"prompt_tokens":100,"completion_tokens":20,"cost":0.004,"is_byok":false,"cost_details":{"upstream_inference_cost":0.003}}`)
	if message.Usage.Cost.Total != 0.004 || message.Usage.Cost.Input != 100 || message.Usage.Cost.Output != 20 || !fromProvider(message.Usage.Cost) {
		t.Fatalf("non-BYOK cost = %+v", message.Usage.Cost)
	}
	// BYOK: OpenRouter's charge plus the upstream inference cost.
	message = stream("openrouter", "https://openrouter.ai/api/v1", `{"prompt_tokens":100,"completion_tokens":20,"cost":0.0002,"is_byok":true,"cost_details":{"upstream_inference_cost":0.003}}`)
	if math.Abs(message.Usage.Cost.Total-0.0032) > 1e-10 || !fromProvider(message.Usage.Cost) {
		t.Fatalf("BYOK cost = %+v", message.Usage.Cost)
	}
	// Another provider that reports the same fields keeps the catalog total.
	message = stream("custom", "https://example.com/v1", `{"prompt_tokens":100,"completion_tokens":20,"cost":0.004,"is_byok":false}`)
	if message.Usage.Cost.Total != 120 || fromProvider(message.Usage.Cost) || message.Usage.Cost.TotalFromProvider == nil {
		t.Fatalf("non-OpenRouter cost = %+v", message.Usage.Cost)
	}
}

// fromProvider reports usage.cost.totalFromProvider as Pi reads it: absent means false.
func fromProvider(cost UsageCost) bool {
	return cost.TotalFromProvider != nil && *cost.TotalFromProvider
}

// openai-completions-provider-stream-event.test.ts "uses OpenRouter's reported total without double-counting non-BYOK upstream cost": a
// model with no catalog rates, so every component is 0 and only the total carries the billed cost.
func TestOpenAICompletionsOpenRouterReportedTotalWithoutCatalogRates(t *testing.T) {
	sse := `data: {"id":"chatcmpl-cost","model":"openrouter/auto","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":20,"cost":0.004,"is_byok":false,"cost_details":{"upstream_inference_cost":0.003}}}` + "\n\ndata: [DONE]\n\n"
	p := NewOpenAIProvider(OpenAIConfig{Model: "openrouter/auto", ProviderID: "openrouter-work", APIKey: "test", BaseURL: "https://openrouter.ai/api/v1"})
	s, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{Fetch: openRouterFetch(sse)})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.Result().Usage.Cost, (UsageCost{Total: 0.004, TotalFromProvider: new(true)}); !reflect.DeepEqual(got, want) {
		t.Fatalf("cost = %+v, want %+v", got, want)
	}
}

func TestAnthropicMessagesUsesOpenRouterReportedCost(t *testing.T) {
	events := anthropicFixtureEvent("message_start", `{"message":{"id":"msg_test","usage":{"input_tokens":12,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`) +
		anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":{"type":"text","text":""}}`) +
		anthropicFixtureEvent("content_block_delta", `{"index":0,"delta":{"type":"text_delta","text":"Hello"}}`) +
		anthropicFixtureEvent("content_block_stop", `{"index":0}`) +
		anthropicFixtureEvent("message_delta", `{"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5,"cost":0.004,"is_byok":false,"cost_details":{"upstream_inference_cost":0.003}}}`) +
		anthropicFixtureEvent("message_stop", `{}`)
	run := func(provider, baseURL string) *AssistantMessage {
		p := NewAnthropicProvider(AnthropicConfig{Model: "anthropic/claude-haiku-4.5", ProviderID: provider, APIKey: "fake-key", BaseURL: baseURL})
		defer func() { _ = p.Close() }()
		s, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}), StreamOptions{Fetch: openRouterFetch(events), ModelCost: ModelCost{Input: 1000000, Output: 1000000}})
		if err != nil {
			t.Fatal(err)
		}
		return s.Result()
	}
	result := run("openrouter-work", "https://openrouter.ai/api/v1")
	if result.Usage.Cost.Total != 0.004 || !fromProvider(result.Usage.Cost) {
		t.Fatalf("total = %v, want 0.004 from the provider (%+v)", result.Usage.Cost.Total, result.Usage.Cost)
	}
	if components := result.Usage.Cost.Input + result.Usage.Cost.Output + result.Usage.Cost.CacheRead + result.Usage.Cost.CacheWrite; components == result.Usage.Cost.Total {
		t.Fatalf("components must stay catalog estimates: %+v", result.Usage.Cost)
	}
	if other := run("custom", "https://example.com"); other.Usage.Cost.Total != other.Usage.Cost.Input+other.Usage.Cost.Output || fromProvider(other.Usage.Cost) {
		t.Fatalf("non-OpenRouter total changed: %+v", other.Usage.Cost)
	}
}
