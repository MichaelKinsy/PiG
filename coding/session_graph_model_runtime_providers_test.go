package coding

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// The Provider object members of a graph ModelRuntime for a provider the extension did not register: an unknown id has no object, a builtin provider has one the host cannot carry and fails with the D78 error (never a partial object), and the display name is answered without the object.
func TestGraphModelRuntimeProviderObjectsWithoutAnExtensionRegistration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw, err := json.Marshal([]any{map[string]any{"authPath": filepath.Join(dir, "auth.json"), "modelsPath": nil, "refreshOnCreate": false}})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := newGraphModelRuntime(t.Context(), &mcpFakeScope{}, "create", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Release)
	call := func(method string, args ...any) (any, error) {
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		return runtime.Call(t.Context(), method, encoded)
	}
	if got, err := call("getProvider", "no-such-provider"); err != nil || got != nil {
		t.Fatalf("getProvider(unknown) = %v, %v; want nothing", got, err)
	}
	if got, err := call("getRegisteredNativeProvider", "anthropic"); err != nil || got != nil {
		t.Fatalf("getRegisteredNativeProvider(builtin) = %v, %v; want nothing", got, err)
	}
	if got, err := call("getProvider", "anthropic"); err == nil || err.Error() != graphProviderCarrierMessage || got != nil {
		t.Fatalf("getProvider(builtin) = %v, %v; want the D78 error", got, err)
	}
	if got, err := call("getProviders"); err == nil || err.Error() != graphProviderCarrierMessage || got != nil {
		t.Fatalf("getProviders = %v, %v; want the D78 error", got, err)
	}
	if got, err := call("providerName", "anthropic"); err != nil || got != "Anthropic" {
		t.Fatalf("providerName(builtin) = %v, %v; want Anthropic", got, err)
	}
	if got, err := call("providerName", "no-such-provider"); err != nil || got != nil {
		t.Fatalf("providerName(unknown) = %v, %v; want nothing", got, err)
	}
}
