package ai

import (
	"reflect"
	"testing"
)

// cloneRadiusModel copies its model once: the result equals the model piMessagesModel builds from a second copy of the gateway view, for every bundled model shape and for empty but present collections.
func TestCloneRadiusModelEqualsTheTwiceCopiedModel(t *testing.T) {
	registryFQOnce.Do(initRegistryFQ)
	registryIDOnce.Do(initRegistryID)
	registryProviderOnce.Do(initRegistryProvider)
	models := make([]*Model, 0, len(GeneratedModels)+1)
	for i := range GeneratedModels {
		models = append(models, GeneratedModels[i].ToModel())
	}
	empty := GeneratedModels[0].ToModel()
	empty.Input, empty.Capabilities.CostTiers = []string{}, []CostTier{}
	empty.ThinkingLevelMap, empty.PromptCache, empty.SamplingParams = ThinkingLevelMap{}, ModelPromptCache{}, SamplingParams{}
	empty.SamplingParamsByThinkingLevel = SamplingParamsByThinkingLevel{"high": {}}
	empty.ProviderMeta.Headers = map[string]string{}
	models = append(models, empty)
	for _, model := range models {
		want := piMessagesModel(model.ProviderMeta.ProviderID, model.ProviderMeta.BaseURL, radiusGatewayModel(model))
		want.catalogRecord, want.catalog = model.catalogRecord, model.catalog
		got := cloneRadiusModel(model)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s/%s: cloneRadiusModel = %+v, want %+v", model.ProviderMeta.ProviderID, model.ID, got, want)
		}
	}
}
