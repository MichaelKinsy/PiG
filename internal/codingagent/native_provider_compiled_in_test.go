package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A compiled-in extension registers its Provider object with API.RegisterNativeProvider; the runtime hands the registry the carrier
// extension.NativeProviderOf builds, which has the object's members and none of a subprocess registration's host callbacks. The registry
// registers it as the object instead of rejecting it as an incomplete subprocess registration.
// upstream: packages/coding-agent/src/core/model-runtime.ts registerNativeProvider (a Provider object registers as it is).
func TestRegisterNativeProviderRegistersACompiledInProviderObject(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	provider := &ai.ModelsProvider{ID: "compiled-in", Name: "Compiled in", GetModels: func() ([]*ai.Model, error) { return nil, nil }}
	if err := registry.RegisterNativeProvider(context.Background(), extension.NativeProviderOf(provider)); err != nil {
		t.Fatalf("RegisterNativeProvider: %v", err)
	}
	got := registry.GetRegisteredNativeProvider(provider.ID)
	if got == nil || got.Name != "Compiled in" || got.GetModels == nil {
		t.Fatalf("registered provider = %+v, want the compiled-in object", got)
	}
}
