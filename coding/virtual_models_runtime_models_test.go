package coding

import (
	"context"
	"reflect"
	"testing"
)

func runtimeModelIDs(runtime *ModelRuntime, provider string) []string {
	var ids []string
	for _, model := range runtime.RuntimeModels() {
		if model.Provider == provider {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

// Startup model resolution reads the runtime's selection metadata, which must list what ModelRuntime.GetModels lists (model-runtime.ts:285-293, virtual-models.ts:190-227): virtual models after their provider's physical models, in registration order, and a provider of only virtual models with no credentials configured (model-runtime.ts:953-958). It must follow registration and removal.
func TestRuntimeModelsListTheVirtualModelsAndTheirAuth(t *testing.T) {
	f := createVirtualRuntime(t)
	route := func(context.Context, ModelRouteRequest) (ModelRoute, error) {
		return ModelRoute{Model: f.runtime.GetModel("faux", "small")}, nil
	}
	for _, definition := range []VirtualModelDefinition{
		{Provider: "faux", ID: "fast", Name: "Fast", Route: route},
		{Provider: "router", ID: "second", Name: "Second", Route: route},
	} {
		if err := f.runtime.RegisterVirtualModel(definition); err != nil {
			t.Fatal(err)
		}
	}
	for _, provider := range []string{"router", "faux"} {
		want := modelIDsOf(f.runtime.GetModels(), provider)
		if got := runtimeModelIDs(f.runtime, provider); !reflect.DeepEqual(got, want) {
			t.Errorf("%s RuntimeModels = %v, want GetModels' %v", provider, got, want)
		}
	}
	if got, want := runtimeModelIDs(f.runtime, "router"), []string{"auto", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("router RuntimeModels = %v, want %v", got, want)
	}
	for _, model := range f.runtime.RuntimeModels() {
		if model.Provider == "router" && model.ID == "auto" && (model.Name != "Auto" || !model.Reasoning) {
			t.Errorf("router/auto metadata = %+v, want name Auto and reasoning (two thinking levels)", model)
		}
	}
	if !f.runtime.HasConfiguredAuth("router") {
		t.Error("a provider of only virtual models needs no credentials, so it is configured")
	}
	if f.runtime.HasConfiguredAuth("no-such-provider") {
		t.Error("an unknown provider reads as configured")
	}

	f.runtime.UnregisterVirtualModel("router", "auto")
	f.runtime.UnregisterVirtualModel("router", "second")
	f.runtime.UnregisterVirtualModel("faux", "fast")
	if got := runtimeModelIDs(f.runtime, "router"); len(got) != 0 {
		t.Errorf("router RuntimeModels after unregister = %v", got)
	}
	if f.runtime.HasConfiguredAuth("router") {
		t.Error("a removed virtual provider still reads as configured")
	}
}
