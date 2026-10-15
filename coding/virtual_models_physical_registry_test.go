package coding

import (
	"context"
	"testing"
)

// Upstream model-runtime.ts getPhysicalModel is `model && !isVirtualModel(model) ? model : undefined`;
// ModelRegistry.unregisterVirtualModel(provider, id) removes that virtual model from the runtime.
// Pi: packages/coding-agent/src/core/model-registry.ts:229 (ModelRegistry.unregisterVirtualModel); packages/coding-agent/src/core/model-runtime.ts:1010 (ModelRuntime.getPhysicalModel).
func TestPhysicalModelLookupAndRegistryUnregisterVirtualModel(t *testing.T) {
	f := createVirtualRuntime(t)
	route := func(context.Context, ModelRouteRequest) (ModelRoute, error) {
		return ModelRoute{Model: f.runtime.GetModel("faux", "small")}, nil
	}
	if err := f.runtime.RegisterVirtualModel(VirtualModelDefinition{Provider: "faux", ID: "fast", Name: "Fast", Route: route}); err != nil {
		t.Fatal(err)
	}
	if f.runtime.GetModel("faux", "fast") == nil {
		t.Fatal("the registered virtual model is not listed")
	}
	if got := f.runtime.GetPhysicalModel("faux", "fast"); got != nil {
		t.Errorf("virtual model reported as physical: %+v", got)
	}
	if got := f.runtime.GetPhysicalModel("faux", "small"); got == nil || got.ID != "small" {
		t.Errorf("physical model = %+v", got)
	}
	if got := f.runtime.GetPhysicalModel("faux", "missing"); got != nil {
		t.Errorf("unknown model = %+v", got)
	}
	(&ModelRegistry{runtime: f.runtime}).UnregisterVirtualModel("faux", "fast")
	if f.runtime.GetModel("faux", "fast") != nil {
		t.Error("ModelRegistry.UnregisterVirtualModel left the virtual model registered")
	}
	(&ModelRegistry{runtime: f.runtime}).UnregisterVirtualModel("faux", "fast") // unknown ids are ignored
	if f.runtime.GetPhysicalModel("faux", "small") == nil {
		t.Error("unregistering a virtual model removed a physical model")
	}
}
