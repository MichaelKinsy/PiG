package subprocess

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi commits a factory's runtime changes only when the factory returns (loader.ts:512-527, initializeExtension 593-610) and checks an MCP server when registerMcpServer is called (loader.ts:456-465), so a factory whose registration is invalid throws and discards every queued change, including an unregisterVirtualModel that would filter another extension's queued model (loader.ts:228-232, 491-494). A register frame whose MCP server the host rejects must therefore leave that queue as it was.
func TestRegisterExtensionAPIRejectedMcpServerLeavesAnotherExtensionsQueuedVirtualModel(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	owner := &managedExt{config: ExtConfig{Name: "owner", Source: "/extensions/owner.mjs"}}
	if err := h.registerExtensionAPI(owner, &RegisterPayload{VirtualModels: []VirtualModelDecl{{Provider: "router", ID: "victim", Name: "Victim"}}}); err != nil {
		t.Fatalf("owner register: %v", err)
	}
	remover := &managedExt{config: ExtConfig{Name: "remover", Source: "/extensions/remover.mjs"}}
	err := h.registerExtensionAPI(remover, &RegisterPayload{
		UnregisterVirtualModels: []VirtualModelRef{{Provider: "router", ID: "victim"}},
		McpServers:              []McpServerDecl{{Name: "bad", Config: json.RawMessage(`{}`)}},
	})
	if err == nil {
		t.Fatal("an MCP server without command or url was accepted")
	}
	if pending := pendingVirtualModels(h.Runtime()); !slices.Equal(pending, []string{"router/victim"}) {
		t.Fatalf("pending virtual models %v after the rejected load, want the owner's router/victim", pending)
	}
	h.mu.Lock()
	tracked := h.virtualModelOwners[VirtualModelRef{Provider: "router", ID: "victim"}]
	h.mu.Unlock()
	if tracked != owner {
		t.Fatalf("router/victim is owned by %v after the rejected load, want the owner", tracked)
	}

	// The same unregistration in a register frame the host accepts filters the owner's model.
	if err := h.registerExtensionAPI(remover, &RegisterPayload{UnregisterVirtualModels: []VirtualModelRef{{Provider: "router", ID: "victim"}}}); err != nil {
		t.Fatalf("remover register: %v", err)
	}
	if pending := pendingVirtualModels(h.Runtime()); len(pending) != 0 {
		t.Fatalf("pending virtual models %v after the accepted load, want none", pending)
	}
}

func pendingVirtualModels(runtime *extension.ExtensionRuntime) []string {
	var pending []string
	for _, registration := range runtime.PendingVirtualModelRegistrations() {
		pending = append(pending, registration.Definition.Provider+"/"+registration.Definition.ID)
	}
	return pending
}
