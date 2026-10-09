package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func aiGatewayCostOf(t *testing.T, pricing string) jsonCost {
	t.Helper()
	var parsed aiGatewayPricing
	if err := json.Unmarshal([]byte(pricing), &parsed); err != nil {
		t.Fatal(err)
	}
	return getAiGatewayCost(&parsed)
}

// Cases mirror packages/ai/test/model-cost-tiers.test.ts "Vercel AI Gateway pricing tiers" (Pi 1.1.0).
func TestVercelAIGatewayPricingTurnsBracketStartsIntoTiersWithTheRatesInEffectThere(t *testing.T) {
	got := aiGatewayCostOf(t, `{"input":"0.000001","output":"0.000005","input_cache_read":"0.0000002",
		"input_tiers":[{"cost":"0.000001","min":0,"max":32001},{"cost":"0.0000018","min":32001,"max":128001},{"cost":"0.000003","min":128001}],
		"output_tiers":[{"cost":"0.000005","min":0,"max":32001},{"cost":"0.000009","min":32001,"max":128001},{"cost":"0.000015","min":128001}]}`)
	want := jsonCost{Input: 1, Output: 5, CacheRead: 0.2, Tiers: []jsonCostTier{
		{InputTokensAbove: 32000, Input: 1.8, Output: 9, CacheRead: 0.2},
		{InputTokensAbove: 128000, Input: 3, Output: 15, CacheRead: 0.2},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cost = %+v, want %+v", got, want)
	}
}

func TestVercelAIGatewayPricingReturnsBaseRatesWithoutTiers(t *testing.T) {
	got := aiGatewayCostOf(t, `{"input":"0.000003","output":0.000015}`)
	want := jsonCost{Input: 3, Output: 15}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cost = %+v, want %+v", got, want)
	}
	if got := getAiGatewayCost(nil); !reflect.DeepEqual(got, jsonCost{}) {
		t.Fatalf("absent pricing = %+v, want zero rates", got)
	}
}
