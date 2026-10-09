package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Pi radius-config.ts:37-45 sanitizeRadiusGatewayConfig (`{ ...model }`) and :61-68 getRadiusModelsFromConfig (`...model`): every
// field a gateway advertises for a model, including types.ts:1133 samplingParamsByThinkingLevel, reaches the Model<"pi-messages">.
// The Go models also own their maps, as cloneRadiusGatewayModel already does for samplingParams and headers.
func TestGetRadiusModelsFromConfigCarriesSamplingParamsByThinkingLevel(t *testing.T) {
	const document = `{"baseUrl":"https://gw.example/v1","models":[
		{"id":"a","type":"chat","name":"A","reasoning":true,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100,
		 "samplingParams":{"temperature":0.5},
		 "samplingParamsByThinkingLevel":{"high":{"temperature":0.2,"top_p":0.9},"low":{"temperature":0.8}}},
		{"id":"b","name":"B","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":10,"maxTokens":1}]}`
	config, ok := sanitizeRadiusGatewayConfig(json.RawMessage(document))
	if !ok {
		t.Fatal("config rejected")
	}
	models := GetRadiusModelsFromConfig("radius-x", config)
	if len(models) != 2 {
		t.Fatalf("models = %d", len(models))
	}
	want := SamplingParamsByThinkingLevel{
		"high": {"temperature": 0.2, "top_p": 0.9},
		"low":  {"temperature": 0.8},
	}
	if !reflect.DeepEqual(models[0].SamplingParamsByThinkingLevel, want) {
		t.Fatalf("a.samplingParamsByThinkingLevel = %#v, want %#v", models[0].SamplingParamsByThinkingLevel, want)
	}
	if models[0].Type != ModelTypeChat || models[1].Type != "" {
		t.Fatalf("types = %q, %q; want chat (advertised) and unset", models[0].Type, models[1].Type)
	}
	if models[1].SamplingParamsByThinkingLevel != nil {
		t.Fatalf("b.samplingParamsByThinkingLevel = %#v, want unset", models[1].SamplingParamsByThinkingLevel)
	}
	encoded, err := EncodeModelsCatalog([]AnyModel{models[0]})
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]json.RawMessage
	if err := json.Unmarshal(encoded[0], &roundTrip); err != nil {
		t.Fatal(err)
	}
	if string(roundTrip["samplingParamsByThinkingLevel"]) != `{"high":{"temperature":0.2,"top_p":0.9},"low":{"temperature":0.8}}` {
		t.Fatalf("serialized = %s", roundTrip["samplingParamsByThinkingLevel"])
	}
	if string(roundTrip["type"]) != `"chat"` {
		t.Fatalf("serialized type = %s, want \"chat\"", roundTrip["type"])
	}
	if _, present := roundTrip["samplingParamsByThinkingLevel"]; !present || models[1].ProviderMeta.API != APIPiMessages || models[1].ProviderMeta.ProviderID != "radius-x" || models[1].ProviderMeta.BaseURL != "https://gw.example/v1" {
		t.Fatalf("model b = %+v", models[1])
	}

	// Mutating a returned model changes neither the config nor the next call's result.
	models[0].SamplingParamsByThinkingLevel["high"]["temperature"] = 99.0
	again := GetRadiusModelsFromConfig("radius-x", config)
	if !reflect.DeepEqual(again[0].SamplingParamsByThinkingLevel, want) {
		t.Fatalf("a returned model aliased the config: %#v", again[0].SamplingParamsByThinkingLevel)
	}
}
