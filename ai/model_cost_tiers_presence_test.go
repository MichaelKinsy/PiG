package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// types.ts ModelCost: tiers is optional, so an authored [] and an absent member are distinct values; JSON.stringify writes []
// and leaves the absent member out. ModelCost keeps that difference as an empty and a nil slice.
func TestModelCostKeepsTiersPresence(t *testing.T) {
	for _, tc := range []struct{ name, wire string }{
		{"absent", `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}`},
		{"empty", `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"tiers":[]}`},
		{"one tier", `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":1000,"input":3,"output":4,"cacheRead":0,"cacheWrite":0}]}`},
	} {
		var cost ModelCost
		if err := json.Unmarshal([]byte(tc.wire), &cost); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(cost)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.wire {
			t.Errorf("%s: ModelCost round trip = %s, want %s", tc.name, got, tc.wire)
		}
	}
}

// provider-composer.ts:233 persists a composed model's cost object as it is, so a model with no catalog record keeps an
// authored tiers: [] in its record and leaves out tiers it never had.
func TestComposedModelRecordKeepsTiersPresence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tiers []CostTier
		want  string
	}{
		{"empty", []CostTier{}, `"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"tiers":[]}`},
		{"absent", nil, `"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},`},
	} {
		model := &Model{ID: "m", DisplayName: "M", ProviderMeta: ProviderMetadata{ProviderID: "p", API: APIOpenAICompletions, BaseURL: "https://example.test"},
			Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 1, MaxOutputTokens: 1, InputCostPer1M: 1, OutputCostPer1M: 2, CostTiers: tc.tiers}}
		records, err := EncodeModelsCatalog([]AnyModel{model})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(records[0]), tc.want) {
			t.Errorf("%s: record = %s, want it to contain %s", tc.name, records[0], tc.want)
		}
	}
}

// generate-models.ts:852-856 copies a fallback model's cost into allowedFallbackModels as it is, and no built-in cost names
// tiers: [], so a built-in fallback cost without tiers has none (a nil Tiers), as Pi's catalog object has none.
func TestBuiltinFallbackCostsWithoutTiersHaveNone(t *testing.T) {
	checked := 0
	for i := range GeneratedModels {
		generated := &GeneratedModels[i]
		if generated.Compat == nil {
			continue
		}
		for _, fallback := range generated.Compat.AllowedFallbackModels {
			checked++
			if len(fallback.Cost.Tiers) == 0 && fallback.Cost.Tiers != nil {
				t.Errorf("%s/%s: fallback %s/%s cost has an empty Tiers, want none", generated.Provider, generated.ID, fallback.Provider, fallback.Model)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no built-in model has allowedFallbackModels")
	}
}
