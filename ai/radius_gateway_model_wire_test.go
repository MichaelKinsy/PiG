package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// upstream: packages/ai/src/providers/radius-config.ts RadiusGatewayModel (lines 6-15) and sanitizeRadiusGatewayConfig (41-49): a gateway's /v1/config model is
// the JSON object {id, name, reasoning, thinkingLevelMap?, input, cost, contextWindow, maxTokens}, and the spread of an accepted model keeps each of those
// members under its exact key. This drives the wire shape, not the Go struct, so a renamed key or a dropped member fails.
func TestRadiusGatewayModelDecodesAndEncodesEveryPiMember(t *testing.T) {
	raw := `{"baseUrl":"https://gw.test","models":[{"id":"radius/fast","name":"Radius Fast","reasoning":true,"thinkingLevelMap":{"minimal":"low","off":null},"input":["text","image"],"cost":{"input":1.5,"output":4.5,"cacheRead":0.25,"cacheWrite":2},"contextWindow":200000,"maxTokens":32000}]}`
	config, ok := sanitizeRadiusGatewayConfig(json.RawMessage(raw))
	if !ok || len(config.Models) != 1 {
		t.Fatalf("sanitize = %+v, %t", config, ok)
	}
	got := config.Models[0]
	low := "low"
	want := RadiusGatewayModel{
		ID: "radius/fast", Name: "Radius Fast", Reasoning: true,
		ThinkingLevelMap: ThinkingLevelMap{"minimal": &low, "off": nil},
		Input:            []string{"text", "image"},
		Cost:             ModelCost{Input: 1.5, Output: 4.5, CacheRead: 0.25, CacheWrite: 2},
		ContextWindow:    200000, MaxTokens: 32000,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded model differs from Pi's members:\n got %#v\nwant %#v", got, want)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]any
	if err := json.Unmarshal(encoded, &members); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "name", "reasoning", "thinkingLevelMap", "input", "cost", "contextWindow", "maxTokens"} {
		if _, present := members[key]; !present {
			t.Errorf("encoded model lacks Pi member %q: %s", key, encoded)
		}
	}
	if members["contextWindow"] != float64(200000) || members["maxTokens"] != float64(32000) || members["name"] != "Radius Fast" {
		t.Errorf("encoded members = %s", encoded)
	}
}
