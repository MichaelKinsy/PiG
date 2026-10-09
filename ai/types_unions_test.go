package ai

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

// Pi types.ts:101 ThinkingTokenBudgetField carries exactly these wire-field literals.
func TestThinkingTokenBudgetFieldLiteralsMatchUpstream(t *testing.T) {
	got := []ThinkingTokenBudgetField{ThinkingTokenBudgetFieldThinkingTokenBudget, ThinkingTokenBudgetFieldThinkingBudget, ThinkingTokenBudgetFieldThinkingBudgetTokens}
	if want := []ThinkingTokenBudgetField{"thinking_token_budget", "thinking_budget", "thinking_budget_tokens"}; !slices.Equal(got, want) {
		t.Fatalf("budget fields %v, want %v", got, want)
	}
}

// Pi types.ts:391 TextSignatureV1 is {v:1,id,phase?}; the Responses signature codec round-trips it.
func TestTextSignatureV1WireShape(t *testing.T) {
	encoded, err := json.Marshal(TextSignatureV1{V: 1, ID: "msg_1", Phase: "commentary"})
	if err != nil || string(encoded) != `{"v":1,"id":"msg_1","phase":"commentary"}` {
		t.Fatalf("encoded %s, err %v", encoded, err)
	}
	if got := encodeResponsesTextSignature("msg_2", ""); got != `{"v":1,"id":"msg_2"}` {
		t.Fatalf("no-phase signature %s", got)
	}
	for signature, want := range map[string][2]string{
		`{"v":1,"id":"a","phase":"final_answer"}`: {"a", "final_answer"},
		`{"v":1,"id":"a","phase":"bogus"}`:        {"a", ""},
		`{"v":2,"id":"a"}`:                        {`{"v":2,"id":"a"}`, ""},
		`legacy_id`:                               {"legacy_id", ""},
	} {
		id, phase, ok := parseResponsesTextSignature(signature)
		if !ok || id != want[0] || phase != want[1] {
			t.Errorf("parse(%s) = %q %q %v, want %v", signature, id, phase, ok, want)
		}
	}
}

// The compat routing and budget-field members decode from catalog JSON into the named types.
func TestOpenAICompatDecodesNamedUnionMembers(t *testing.T) {
	var compat OpenAICompat
	data := `{"thinkingTokenBudgetField":"thinking_budget","openRouterRouting":{"order":["a"],"extra":1},"vercelGatewayRouting":{"only":["b"]}}`
	if err := json.Unmarshal([]byte(data), &compat); err != nil {
		t.Fatal(err)
	}
	if compat.ThinkingTokenBudgetField != ThinkingTokenBudgetFieldThinkingBudget || compat.OpenRouterRouting["extra"] != float64(1) || compat.VercelGatewayRouting == nil || !slices.Equal(compat.VercelGatewayRouting.Only, []string{"b"}) {
		t.Fatalf("decoded %+v", compat)
	}
}

// Pi types.ts GrammarVariants (Partial<Record<GrammarFormat,string>>) is the constrained-sampling variants map.
func TestGrammarVariantsAndRoutingAliasesAreTheFieldTypes(t *testing.T) {
	variants := GrammarVariants{"lark": "start: x"}
	sampling := ConstrainedSamplingConfig{Variants: variants}
	if sampling.Variants["lark"] != "start: x" {
		t.Fatalf("variants %v", sampling.Variants)
	}
	routing := OpenRouterRouting{"order": []any{"a"}}
	vercel := &VercelGatewayRouting{Only: []string{"b"}}
	compat := OpenAICompat{OpenRouterRouting: routing, VercelGatewayRouting: vercel}
	if compat.OpenRouterRouting["order"] == nil || compat.VercelGatewayRouting.Only == nil {
		t.Fatalf("compat %+v", compat)
	}
}

// Pi api/anthropic-messages.ts AnthropicEffort: the five output-config effort levels are accepted, anything else is not.
func TestAnthropicEffortAcceptsExactlyUpstreamLevels(t *testing.T) {
	for _, level := range []AnthropicEffort{AnthropicEffortLow, AnthropicEffortMedium, AnthropicEffortHigh, AnthropicEffortXHigh, AnthropicEffortMax} {
		if !isAnthropicEffort(level) {
			t.Errorf("%q rejected", level)
		}
	}
	for _, level := range []string{"", "minimal", "off", "LOW"} {
		if isAnthropicEffort(AnthropicEffort(level)) {
			t.Errorf("%q accepted", level)
		}
	}
}

// Pi types.ts ChatTemplateKwargValue admits scalars and a `$var` placeholder object; the catalog field keeps both.
func TestChatTemplateKwargValueKeepsScalarsAndPlaceholders(t *testing.T) {
	var compat OpenAICompat
	if err := json.Unmarshal([]byte(`{"chatTemplateKwargs":{"a":"x","b":1,"c":true,"d":null,"e":{"$var":"thinking.enabled"}}}`), &compat); err != nil {
		t.Fatal(err)
	}
	if reflect.TypeOf(compat.ChatTemplateKwargs).Elem() != reflect.TypeFor[ChatTemplateKwargValue]() {
		t.Fatalf("chatTemplateKwargs values are %v, want ChatTemplateKwargValue", reflect.TypeOf(compat.ChatTemplateKwargs).Elem())
	}
	e := compat.ChatTemplateKwargs["e"]
	if compat.ChatTemplateKwargs["a"] != "x" || compat.ChatTemplateKwargs["b"] != float64(1) || compat.ChatTemplateKwargs["c"] != true || compat.ChatTemplateKwargs["d"] != nil || e.(map[string]any)["$var"] != "thinking.enabled" {
		t.Fatalf("decoded %v", compat.ChatTemplateKwargs)
	}
}
