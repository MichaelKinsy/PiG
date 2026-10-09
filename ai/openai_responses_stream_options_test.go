package ai

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

// openai-responses-shared.ts:578-583 processResponsesStream: with applyServiceTierPricing set, the tier that prices a response is
// `response.service_tier ?? options.serviceTier` (or the resolveServiceTier result), and openai-responses.ts:194-198 passes the request's
// serviceTier. A response that omits service_tier is therefore priced at the requested tier on OpenAI Responses too, not only on Codex.
func TestResponsesPricesAResponseWithoutServiceTierAtTheRequestedTier(t *testing.T) {
	request := Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}
	for _, tc := range []struct {
		id, requested, returned string
		multiplier              float64
	}{
		{"gpt-5.4", "priority", "", 2},
		{"gpt-5.5", "priority", "", 2.5},
		{"gpt-5.5", "flex", "", 0.5},
		{"gpt-5.5", "flex", "priority", 2.5}, // a returned tier wins over the requested one
		{"gpt-5.5", "", "", 1},
	} {
		t.Run(fmt.Sprintf("%s requested %q returned %q", tc.id, tc.requested, tc.returned), func(t *testing.T) {
			model, _ := LookupModelExact("openai/" + tc.id)
			options := StreamOptions{ModelCost: (&Model{Capabilities: model.ToCapabilities()}).CostRates(), ServiceTier: tc.requested}
			tier := ""
			if tc.returned != "" {
				tier = fmt.Sprintf(`"service_tier":%q,`, tc.returned)
			}
			reply := `data: {"type":"response.completed","response":{"status":"completed",` + tier + `"usage":{"input_tokens":100000,"output_tokens":100000,"total_tokens":200000,"input_tokens_details":{"cached_tokens":0}}}}` + "\n\n"
			_, _, result := captureResponsesCompat(t, responsesCompatConfig(t, "openai", tc.id), request, options, reply)
			scale := float64(100000) / 1000000
			want := (model.InputCostPerMTokens + model.OutputCostPerMTokens) * tc.multiplier * scale
			if math.Abs(result.Usage.Cost.Total-want) >= 5e-13 {
				t.Errorf("total cost = %v, want %v (multiplier %v)", result.Usage.Cost.Total, want, tc.multiplier)
			}
		})
	}
}

// openai-responses-shared.ts:110-122,578-583,600 OpenAIResponsesStreamOptions: processResponsesStream calls onProviderStreamEvent for each
// event, and with applyServiceTierPricing set it passes the resolveServiceTier result (or `response ?? request`) to it; without
// applyServiceTierPricing no pricing runs.
func TestProcessResponsesStreamCallsTheInjectedOptions(t *testing.T) {
	const events = `data: {"type":"response.created","response":{"id":"r1"}}` + "\n\n" +
		`data: {"type":"response.completed","response":{"status":"completed","service_tier":"default","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}` + "\n\n"
	run := func(options *OpenAIResponsesStreamOptions) *AssistantMessage {
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "openai", "gpt-test")
		go processResponsesStream(t.Context(), newSSEDecoder(strings.NewReader(events)), builder, options)
		return builder.stream.Result()
	}
	var seen []string
	var resolved [][2]string
	var priced []string
	result := run(&OpenAIResponsesStreamOptions{
		model: &Model{ID: "gpt-test"},
		OnProviderStreamEvent: func(ctx context.Context, data any, model *Model) error {
			if ctx == nil || model == nil || model.ID != "gpt-test" {
				t.Errorf("observer got ctx=%v model=%+v, want the request context and the model processResponsesStream was given", ctx, model)
			}
			seen = append(seen, data.(map[string]any)["type"].(string))
			return nil
		},
		ServiceTier: "flex",
		ResolveServiceTier: func(response, request string) string {
			resolved = append(resolved, [2]string{response, request})
			return "resolved"
		},
		ApplyServiceTierPricing: func(usage *Usage, serviceTier string) {
			priced = append(priced, serviceTier)
			usage.Cost.Total = 42
		},
	})
	if !reflect.DeepEqual(seen, []string{"response.created", "response.completed"}) {
		t.Errorf("observed events = %v", seen)
	}
	if !reflect.DeepEqual(resolved, [][2]string{{"default", "flex"}}) || !reflect.DeepEqual(priced, []string{"resolved"}) {
		t.Errorf("resolved = %v, priced = %v", resolved, priced)
	}
	if result.Usage.Cost.Total != 42 || result.StopReason != StopReasonStop {
		t.Errorf("result = %+v", result)
	}

	var fallbackPriced []string
	run(&OpenAIResponsesStreamOptions{ServiceTier: "flex", ApplyServiceTierPricing: func(_ *Usage, serviceTier string) { fallbackPriced = append(fallbackPriced, serviceTier) }})
	if !reflect.DeepEqual(fallbackPriced, []string{"default"}) {
		t.Errorf("without ResolveServiceTier the response's tier wins: %v", fallbackPriced)
	}
	if result := run(&OpenAIResponsesStreamOptions{ServiceTier: "flex"}); result.Usage.Cost.Total != 0 {
		t.Errorf("no ApplyServiceTierPricing must leave the default rate: %+v", result.Usage.Cost)
	}
}

// openai-codex-responses.ts:631-639 resolveCodexServiceTier: a response that reports "default" for a flex or priority request is priced at the
// requested tier; otherwise the response's tier, then the request's.
func TestResolveCodexServiceTier(t *testing.T) {
	for _, tc := range []struct{ response, request, want string }{
		{"default", "flex", "flex"},
		{"default", "priority", "priority"},
		{"default", "", "default"},
		{"default", "fast", "default"},
		{"flex", "priority", "flex"},
		{"", "priority", "priority"},
		{"", "", ""},
		{"fast", "", "fast"},
	} {
		if got := resolveCodexServiceTier(tc.response, tc.request); got != tc.want {
			t.Errorf("resolveCodexServiceTier(%q, %q) = %q, want %q", tc.response, tc.request, got, tc.want)
		}
	}
}
