package modelgen

// pi: packages/ai/catalog/sources/models-dev.ts
// No upstream test covers this file; the cases pin the v2 price shapes normalizeCost and modelsDevCost read.

import (
	"math"
	"testing"
)

func costOf(t *testing.T, cost string) *Obj {
	t.Helper()
	return ModelsDevToCost(NormalizeModelsDevModel(parseObj(t, `{"id":"m","type":"chat","cost":`+cost+`}`)).Cost)
}

func TestModelsDevCostTiers(t *testing.T) {
	t.Run("context overrides with gt and gte become tiers; other overrides are ignored", func(t *testing.T) {
		requireJSON(t, costOf(t, `{"text":{
			"input":{"price":3,"cache":{"read":0.3,"write":3.75},"overrides":[
				{"when":{"context":{"gt":200000}},"price":6,"cache":{"read":0.6,"write":7.5}},
				{"when":{"context":{"gte":500000}},"price":9},
				{"when":{"region":"eu"},"price":99},
				{"when":{"context":{"gt":1},"tier":"priority"},"price":98},
				{"when":{"context":{"lt":5}},"price":97},
				{"when":{"context":{"gt":"big"}},"price":96}]},
			"output":{"price":15,"overrides":[
				{"when":{"context":{"gt":200000}},"price":22.5},
				{"when":{"context":{"gte":900000}},"price":30}]}}}`),
			`{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75,"tiers":[
				{"inputTokensAbove":200000,"input":6,"output":22.5,"cacheRead":0.6,"cacheWrite":7.5},
				{"inputTokensAbove":500000,"input":9,"output":15,"cacheRead":0.3,"cacheWrite":3.75},
				{"inputTokensAbove":900000,"input":3,"output":30,"cacheRead":0.3,"cacheWrite":3.75}]}`)
	})
	t.Run("a threshold given by gt wins over gte", func(t *testing.T) {
		requireJSON(t, costOf(t, `{"text":{"input":{"price":1,"overrides":[{"when":{"context":{"gt":10,"gte":20}},"price":2}]},"output":{"price":1}}}`),
			`{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":10,"input":2,"output":1,"cacheRead":0,"cacheWrite":0}]}`)
	})
	t.Run("input and output overrides of one threshold share a tier, in first-seen order", func(t *testing.T) {
		requireJSON(t, costOf(t, `{"text":{"input":{"price":1,"overrides":[{"when":{"context":{"gt":50}},"price":2}]},"output":{"price":1,"overrides":[{"when":{"context":{"gt":10}},"price":5},{"when":{"context":{"gt":50}},"price":6}]}}}`),
			`{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"tiers":[
				{"inputTokensAbove":50,"input":2,"output":6,"cacheRead":0,"cacheWrite":0},
				{"inputTokensAbove":10,"input":1,"output":5,"cacheRead":0,"cacheWrite":0}]}`)
	})
}

func TestModelsDevCacheWritePerTTLPicksFirstValue(t *testing.T) {
	// models.dev lists one cache write price per TTL; the first is the host's default.
	requireJSON(t, costOf(t, `{"text":{"input":{"price":3,"cache":{"read":0.3,"write":{"1h":6,"5m":3.75}}},"output":{"price":15}}}`), `{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":6}`)
	// A plain number is the price itself.
	requireJSON(t, costOf(t, `{"text":{"input":{"price":3,"cache":{"write":3.75}},"output":{"price":15}}}`), `{"input":3,"output":15,"cacheRead":0,"cacheWrite":3.75}`)
	// Without a write price the explicit-cache write price is used; an empty per-TTL map counts as missing.
	requireJSON(t, costOf(t, `{"text":{"input":{"price":3,"cache":{"explicit":{"write":{"5m":4}}}},"output":{"price":15}}}`), `{"input":3,"output":15,"cacheRead":0,"cacheWrite":4}`)
	requireJSON(t, costOf(t, `{"text":{"input":{"price":3,"cache":{"write":{},"explicit":{"write":5}}},"output":{"price":15}}}`), `{"input":3,"output":15,"cacheRead":0,"cacheWrite":5}`)
	// Per-TTL write prices inside an override use the same rule.
	requireJSON(t, costOf(t, `{"text":{"input":{"price":3,"cache":{"write":3},"overrides":[{"when":{"context":{"gt":9}},"cache":{"write":{"1h":8,"5m":7}}}]},"output":{"price":15}}}`),
		`{"input":3,"output":15,"cacheRead":0,"cacheWrite":3,"tiers":[{"inputTokensAbove":9,"input":3,"output":15,"cacheRead":0,"cacheWrite":8}]}`)
}

func TestModelsDevNonTokenUnitsYieldAnEmptyCost(t *testing.T) {
	empty := `{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`
	for _, tc := range []struct{ name, cost string }{
		{"input per image", `{"text":{"input":{"unit":"image","price":3},"output":{"price":15}}}`},
		{"output per second", `{"text":{"input":{"price":3},"output":{"unit":"second","price":15}}}`},
		{"no output", `{"text":{"input":{"price":3}}}`},
		{"no input", `{"text":{"output":{"price":3}}}`},
		{"no text", `{"image":{"input":{"price":3},"output":{"price":15}}}`},
		{"no cost", `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireJSON(t, costOf(t, tc.cost), empty)
		})
	}
	// The token unit may be spelled out.
	requireJSON(t, costOf(t, `{"text":{"input":{"unit":"token","price":3},"output":{"unit":"token","price":15}}}`), `{"input":3,"output":15,"cacheRead":0,"cacheWrite":0}`)
}

func TestModelsDevToCostMissingRatesAreFree(t *testing.T) {
	one, nan := 1.0, math.NaN()
	requireJSON(t, ModelsDevToCost(ModelsDevCost{}), `{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`)
	requireJSON(t, ModelsDevToCost(ModelsDevCost{Input: &one, CacheRead: &nan}), `{"input":1,"output":0,"cacheRead":0,"cacheWrite":0}`)
	// A tier rate that is present, even 0, replaces the base rate; a missing one keeps it.
	zero := 0.0
	requireJSON(t, ModelsDevToCost(ModelsDevCost{Input: &one, Output: &one, Tiers: []ModelsDevCostTier{{Above: 5, Input: &zero}}}),
		`{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":5,"input":0,"output":1,"cacheRead":0,"cacheWrite":0}]}`)
}

func TestNormalizeModelsDevModel(t *testing.T) {
	model := NormalizeModelsDevModel(parseObj(t, `{"id":"m","name":"M","type":"chat","status":"deprecated",
		"capabilities":{"tools":{"supported":true},"structured_output":true,"reasoning":{"supported":true,"toggle":true,"effort":["low",null,"high"],"budget":{"min":1024,"max":32000}}},
		"modalities":{"input":["text","image/png"],"output":["text"]},"limit":{"context":200000,"output":8000},
		"api":{"responses":{"base_url":"https://a.test","sdk":{"aisdk":"@ai-sdk/openai"}},"chat":{"base_url":"https://b.test"}}}`))
	if model.ID != "m" || model.Name != "M" || model.Type != "chat" || model.Status != "deprecated" || !model.ToolCall || !model.StructuredOutput || !model.Reasoning ||
		!model.ImageInput || !model.TextInput || !model.TextOutput || model.ContextWindow != 200000 || model.MaxTokens != 8000 {
		t.Fatalf("model = %+v", model)
	}
	if len(model.ReasoningOptions) != 3 || model.ReasoningOptions[0].Type != "toggle" || model.ReasoningOptions[1].Type != "effort" || model.ReasoningOptions[2].Type != "budget_tokens" ||
		len(model.ReasoningOptions[1].Values) != 3 || model.ReasoningOptions[1].Values[1] != nil || *model.ReasoningOptions[1].Values[2] != "high" ||
		*model.ReasoningOptions[2].Min != 1024 || *model.ReasoningOptions[2].Max != 32000 || !model.HasEffortOption() || !model.HasToggleOption() {
		t.Fatalf("reasoning options = %+v", model.ReasoningOptions)
	}
	if len(model.APIs) != 2 || model.APIs[0] != (ModelsDevAPI{"responses", "https://a.test", "@ai-sdk/openai"}) || model.APIs[1] != (ModelsDevAPI{"chat", "https://b.test", ""}) {
		t.Fatalf("apis = %+v", model.APIs)
	}

	bare := NormalizeModelsDevModel(parseObj(t, `{"id":"b","reasoning":{}}`))
	if bare.ToolCall || bare.Reasoning || bare.ReasoningOptions != nil || bare.ImageInput || bare.TextInput || bare.ContextWindow != 0 || len(bare.APIs) != 0 {
		t.Fatalf("bare = %+v", bare)
	}
	// Reasoning options exist only when the model reasons.
	unsupported := NormalizeModelsDevModel(parseObj(t, `{"id":"u","capabilities":{"reasoning":{"supported":false,"toggle":true}}}`))
	if unsupported.Reasoning || unsupported.ReasoningOptions != nil {
		t.Fatalf("unsupported = %+v", unsupported)
	}
}

func TestModelsDevChatModelsKeepsOnlyChatTypeInOrder(t *testing.T) {
	catalog := parseObj(t, `{"p":{"models":{"b":{"id":"b","type":"chat"},"img":{"id":"img","type":"image"},"a":{"id":"a","type":"chat"},"untyped":{"id":"untyped"}}}}`)
	var ids []string
	for _, model := range ModelsDevChatModels(catalog, "p") {
		ids = append(ids, model.ID)
	}
	if len(ids) != 2 || ids[0] != "b" || ids[1] != "a" {
		t.Fatalf("chat models = %v", ids)
	}
	if got := ModelsDevChatModels(catalog, "missing"); got != nil {
		t.Fatalf("missing provider = %v", got)
	}
}

// RoundCost is Number(value.toFixed(6)) as evaluated by Node 24: ties round away from zero on the exact binary value.
func TestRoundCostMatchesToFixed(t *testing.T) {
	for _, row := range []struct{ in, want float64 }{
		{0.0078125, 0.007813}, {-0.0078125, -0.007813}, {1.0000005, 1.000001}, {0.000000042 * 1e6, 0.042}, {2.5e-7 * 1e6, 0.25}, {0, 0},
		{1e21, 1e21}, {123456789.1234567, 123456789.123457},
	} {
		if got := RoundCost(row.in); got != row.want {
			t.Errorf("RoundCost(%v) = %v, want %v", row.in, got, row.want)
		}
	}
	if got := RoundCost(math.Inf(1)); !math.IsInf(got, 1) {
		t.Errorf("RoundCost(+Inf) = %v", got)
	}
	if got := RoundCost(math.NaN()); !math.IsNaN(got) {
		t.Errorf("RoundCost(NaN) = %v", got)
	}
}
