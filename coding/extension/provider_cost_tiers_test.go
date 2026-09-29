package extension

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestProviderRegistrationCostTiersRoundTrip(t *testing.T) {
	// extensions-runner.test.ts:50-79,1173-1195 registers this model after bind and requires its complete tier.
	const input = `{"baseUrl":"https://provider.test/v1","apiKey":"provider-test-key","api":"openai-completions","models":[{"id":"instant-model","name":"Instant Model","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0.1,"cacheWrite":1.25,"tiers":[{"inputTokensAbove":272000,"input":2,"output":3,"cacheRead":0.2,"cacheWrite":2.5}]},"contextWindow":128000,"maxTokens":4096}]}`
	var config ProviderConfig
	if err := json.Unmarshal([]byte(input), &config); err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var before, after struct {
		Models []struct {
			Cost map[string]any `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(input), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Models[0].Cost, before.Models[0].Cost) {
		t.Fatalf("registration changed cost: got %v want %v", after.Models[0].Cost, before.Models[0].Cost)
	}
}
