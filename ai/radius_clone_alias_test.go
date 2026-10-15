package ai

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// sharedPaths lists the paths at which a and b reach the same slice backing array, map or pointee.
func sharedPaths(a, b reflect.Value, path string, out *[]string) {
	if a.Kind() != b.Kind() {
		return
	}
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return
		}
		if a.Pointer() == b.Pointer() {
			*out = append(*out, path)
			return
		}
		sharedPaths(a.Elem(), b.Elem(), path+".*", out)
	case reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return
		}
		sharedPaths(a.Elem(), b.Elem(), path, out)
	case reflect.Struct:
		for i := range a.NumField() {
			sharedPaths(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name, out)
		}
	case reflect.Slice:
		if a.Len() == 0 || b.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			*out = append(*out, path)
			return
		}
		for i := range min(a.Len(), b.Len()) {
			sharedPaths(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i), out)
		}
	case reflect.Map:
		if a.IsNil() || b.IsNil() {
			return
		}
		if a.Pointer() == b.Pointer() {
			*out = append(*out, path)
			return
		}
		for _, key := range a.MapKeys() {
			if value := b.MapIndex(key); value.IsValid() {
				sharedPaths(a.MapIndex(key), value, fmt.Sprintf("%s[%v]", path, key), out)
			}
		}
	}
}

// cloneRadiusModel's result shares no collection with its source beyond what the copy through piMessagesModel shares: once radiusGatewayModel's copy is the only one, a caller that edits the clone must still leave the published model alone.
func TestCloneRadiusModelSharesNoCollectionWithItsSource(t *testing.T) {
	registryFQOnce.Do(initRegistryFQ)
	registryIDOnce.Do(initRegistryID)
	registryProviderOnce.Do(initRegistryProvider)
	models := make([]*Model, 0, len(GeneratedModels)+1)
	for i := range GeneratedModels {
		models = append(models, GeneratedModels[i].ToModel())
	}
	// Every collection non-empty, so the check bites on each slice and map.
	full := GeneratedModels[0].ToModel()
	high := "high"
	full.Input = []string{"text", "image"}
	full.ProviderMeta.Headers = map[string]string{"x": "y"}
	full.ThinkingLevelMap = ThinkingLevelMap{"high": &high}
	full.SamplingParams = SamplingParams{"temperature": 0.5}
	full.SamplingParamsByThinkingLevel = SamplingParamsByThinkingLevel{"high": {"top_p": 0.9}}
	full.PromptCache = ModelPromptCache{"ttl": 300}
	full.Capabilities.CostTiers = []CostTier{{}}
	models = append(models, full)
	for _, model := range models {
		reference := piMessagesModel(model.ProviderMeta.ProviderID, model.ProviderMeta.BaseURL, radiusGatewayModel(model))
		var allowed, got []string
		sharedPaths(reflect.ValueOf(model).Elem(), reflect.ValueOf(reference).Elem(), "model", &allowed)
		sharedPaths(reflect.ValueOf(model).Elem(), reflect.ValueOf(cloneRadiusModel(model)).Elem(), "model", &got)
		for _, path := range got {
			if path != "model.catalog" && !slices.Contains(allowed, path) {
				t.Fatalf("%s/%s: cloneRadiusModel shares %s with its source", model.ProviderMeta.ProviderID, model.ID, path)
			}
		}
	}
}
