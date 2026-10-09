package extension

import (
	"encoding/json"
	"testing"
)

// ExtensionRuntime.mcpServers is the McpServerRegistry the runtime's registerMcpServer/unregisterMcpServer write to, so one registry serves the runner, the host and the SDK state push: a server registered through the runtime is visible in the registry (get/list), a foreign unregister leaves it alone, the owner's removes it, and each change calls the registry's change listener once.
//
// mutation-checked: McpServers returned a fresh registry (list/get empty), UnregisterMcpServer passed another extension path (the owner's unregister left the server), SetMcpServersChangeListener did not reach the registry (zero changes).
// upstream: packages/coding-agent/src/core/extensions/types.ts:2138 (mcpServers: McpServerRegistry), packages/coding-agent/src/core/mcp-servers.ts:316-341 (register, unregister, get, list)
func TestRuntimeMcpServersIsTheRegistryTheRuntimeWritesTo(t *testing.T) {
	runtime := CreateExtensionRuntime()
	changes := 0
	runtime.SetMcpServersChangeListener(func() { changes++ })
	if err := runtime.RegisterMcpServer("/ext/a.mjs", "docs", json.RawMessage(`{"url":"https://docs.example/mcp"}`)); err != nil {
		t.Fatal(err)
	}
	registry := runtime.McpServers()
	if registry != runtime.McpServers() {
		t.Fatal("McpServers returned a different registry on the second call")
	}
	got, ok := registry.Get("docs")
	if !ok || got.ExtensionPath != "/ext/a.mjs" || got.Config.URL != "https://docs.example/mcp" || changes != 1 {
		t.Fatalf("Get(docs) = %+v, %v after %d changes", got, ok, changes)
	}
	registry.Unregister("docs", "/ext/other.mjs")
	if list := registry.List(); len(list) != 1 || changes != 1 {
		t.Fatalf("a foreign unregister changed the registry: %+v after %d changes", list, changes)
	}
	runtime.UnregisterMcpServer("/ext/a.mjs", "docs")
	if _, ok := registry.Get("docs"); ok || len(registry.List()) != 0 || changes != 2 {
		t.Fatalf("the owner's unregister left %v, %d changes", registry.List(), changes)
	}
}
