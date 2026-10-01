package subprocess

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func loadRegistering(t *testing.T, host *Host, name string) *wireExt {
	t.Helper()
	w := newWireExt(t)
	if _, err := loadWireExt(t, host, w, &RegisterPayload{Name: name,
		McpServers:    []McpServerDecl{mcpDecl("docs", "http://docs.invalid")},
		VirtualModels: []VirtualModelDecl{{Provider: "router", ID: "auto", Name: "Auto", ThinkingLevels: []string{"off"}}},
	}); err != nil {
		t.Fatal(err)
	}
	return w
}

// Upstream registers MCP servers and virtual models per loaded extension and drops them with it (loader.ts:259-262, runner.ts invalidate); an extension that stops must not leave them registered.
func TestStoppingAnExtensionUnregistersItsMcpServersAndVirtualModels(t *testing.T) {
	host := newWireHost(t)
	loadRegistering(t, host, "plugin")
	if len(host.Runtime().McpServers()) != 1 || len(host.Runtime().PendingVirtualModelRegistrations()) != 1 {
		t.Fatal("registrations missing before stop")
	}
	host.mu.Lock()
	me := host.exts["plugin"]
	host.mu.Unlock()
	host.stopManaged(me, "test stop")
	if got := host.Runtime().McpServers(); len(got) != 0 {
		t.Fatalf("servers after stop = %+v", got)
	}
	if got := host.Runtime().PendingVirtualModelRegistrations(); len(got) != 0 {
		t.Fatalf("virtual models after stop = %+v", got)
	}
}

// A reload registers the successor before it stops the replaced generation, so the predecessor's stop must leave what the successor registered (the providers' keep set does the same), while a registration the successor dropped goes.
func TestReplacingAnExtensionKeepsTheSuccessorsRegistrations(t *testing.T) {
	host := newWireHost(t)
	loadRegistering(t, host, "plugin")
	host.mu.Lock()
	old := host.exts["plugin"]
	host.mu.Unlock()

	w := newWireExt(t)
	if _, err := loadWireExt(t, host, w, &RegisterPayload{Name: "plugin", McpServers: []McpServerDecl{mcpDecl("docs", "http://docs2.invalid")}}); err != nil {
		t.Fatal(err)
	}
	host.mu.Lock()
	replacement := host.exts["plugin"]
	host.mu.Unlock()
	if replacement == old {
		t.Fatal("the load did not replace the extension")
	}
	servers := host.Runtime().McpServers()
	if len(servers) != 1 || servers[0].Config.URL != "http://docs2.invalid" {
		t.Fatalf("servers after replacement = %+v", servers)
	}
	if got := host.Runtime().PendingVirtualModelRegistrations(); len(got) != 0 {
		t.Fatalf("the replaced generation's virtual model survived: %+v", got)
	}
}

// A packed member that is disabled leaves the extension set; its registrations go with it, as its providers do.
func TestDisabledMemberUnregistersItsMcpServersAndVirtualModels(t *testing.T) {
	host := newWireHost(t)
	loadRegistering(t, host, "plugin")
	host.mu.Lock()
	me := host.exts["plugin"]
	host.mu.Unlock()
	host.disablePackedMember(me, "test disable")
	if got := host.Runtime().McpServers(); len(got) != 0 {
		t.Fatalf("servers after disable = %+v", got)
	}
	if got := host.Runtime().PendingVirtualModelRegistrations(); len(got) != 0 {
		t.Fatalf("virtual models after disable = %+v", got)
	}
}

// Re-registering a provider and id replaces the virtual model whichever extension registers it (model-runtime.ts:939-952), so the registry holds the last registrant's definition. Stopping the earlier registrant must not remove the model the later one registered; stopping the later one removes it.
func TestStoppingAnEarlierRegistrantLeavesAnotherExtensionsVirtualModel(t *testing.T) {
	host := newWireHost(t)
	var unregistered []string
	host.Runtime().BindProviderActions(extension.ProviderActions{
		RegisterVirtualModel:   func(extension.VirtualModelDefinition) error { return nil },
		UnregisterVirtualModel: func(provider, id string) { unregistered = append(unregistered, provider+"/"+id) },
	}, nil)
	route := VirtualModelDecl{Provider: "router", ID: "auto", Name: "Auto"}
	for _, name := range []string{"first", "second"} {
		if _, err := loadWireExt(t, host, newWireExt(t), &RegisterPayload{Name: name, VirtualModels: []VirtualModelDecl{route}}); err != nil {
			t.Fatal(err)
		}
	}
	host.mu.Lock()
	first, second := host.exts["first"], host.exts["second"]
	host.mu.Unlock()
	host.stopManaged(first, "test stop")
	if len(unregistered) != 0 {
		t.Fatalf("stopping the earlier registrant unregistered %v, which the other extension registered last", unregistered)
	}
	host.stopManaged(second, "test stop")
	if len(unregistered) != 1 || unregistered[0] != "router/auto" {
		t.Fatalf("stopping the owner unregistered %v, want [router/auto]", unregistered)
	}
}
