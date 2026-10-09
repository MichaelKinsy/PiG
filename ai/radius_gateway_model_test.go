package ai

import (
	"reflect"
	"testing"
)

// upstream: packages/ai/src/providers/radius-config.ts RadiusGatewayModel (lines 6-15) and getRadiusModelsFromConfig (61-68): every gateway model field (id, name, reasoning, thinkingLevelMap, input, cost, contextWindow, maxTokens) is spread into a Model<"pi-messages"> that takes api "pi-messages", the caller's provider id and the gateway base URL.
func TestRadiusGatewayModelFieldsReachThePiMessagesModel(t *testing.T) {
	off := "off"
	gateway := RadiusGatewayModel{
		ID: "radius/fast", Name: "Radius Fast", Reasoning: true,
		ThinkingLevelMap: ThinkingLevelMap{"minimal": &off},
		Input:            []string{"text", "image"},
		Cost:             ModelCost{Input: 1.5, Output: 4.5, CacheRead: 0.25, CacheWrite: 2},
		ContextWindow:    200000, MaxTokens: 32000,
	}
	models := GetRadiusModelsFromConfig("radius-provider", RadiusGatewayConfig{BaseURL: "https://gateway.test", Models: []RadiusGatewayModel{gateway}})
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	model := models[0]
	if model.ProviderMeta.API != APIPiMessages || model.ProviderMeta.ProviderID != "radius-provider" || model.ProviderMeta.BaseURL != "https://gateway.test" {
		t.Errorf("api/provider/baseUrl = %q/%q/%q, want pi-messages/radius-provider/https://gateway.test", model.ProviderMeta.API, model.ProviderMeta.ProviderID, model.ProviderMeta.BaseURL)
	}
	if model.ID != "radius/fast" || model.DisplayName != "Radius Fast" || !model.ProviderMeta.Reasoning {
		t.Errorf("id/name/reasoning = %q/%q/%v", model.ID, model.DisplayName, model.ProviderMeta.Reasoning)
	}
	if !reflect.DeepEqual(model.Input, []string{"text", "image"}) {
		t.Errorf("input = %v", model.Input)
	}
	if got := model.ThinkingLevelMap["minimal"]; got == nil || *got != "off" {
		t.Errorf("thinkingLevelMap[minimal] = %v, want off", got)
	}
	capabilities := model.Capabilities
	if capabilities.InputCostPer1M != 1.5 || capabilities.OutputCostPer1M != 4.5 || capabilities.CacheReadCostPer1M != 0.25 || capabilities.CacheWriteCostPer1M != 2 {
		t.Errorf("cost = %v/%v/%v/%v, want 1.5/4.5/0.25/2", capabilities.InputCostPer1M, capabilities.OutputCostPer1M, capabilities.CacheReadCostPer1M, capabilities.CacheWriteCostPer1M)
	}
	if capabilities.ContextWindow != 200000 || capabilities.MaxOutputTokens != 32000 {
		t.Errorf("contextWindow/maxTokens = %d/%d, want 200000/32000", capabilities.ContextWindow, capabilities.MaxOutputTokens)
	}
	if round := radiusGatewayModel(model); !reflect.DeepEqual(round, cloneRadiusGatewayModel(gateway)) {
		t.Errorf("the Model does not round-trip to its gateway model:\n got %#v\nwant %#v", round, gateway)
	}
}
