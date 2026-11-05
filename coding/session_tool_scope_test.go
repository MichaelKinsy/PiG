package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// scopeProbeTools registers an ordinary tool and two tools of one MCP-like namespace, one of them late (at session_start).
func scopeProbeTools(_ *Session, registered map[string]extension.RegisteredTool) {
	registerPortTool(registered, dynamicRegistryTool())
	for _, name := range []string{"mcp__docs__a", "mcp__docs__b"} {
		definition := registryTool(name, name, "an MCP probe", "probe "+name)
		definition.Namespace = &extension.ToolNamespace{Name: "mcp__docs"}
		registerPortTool(registered, definition)
	}
}

// The Session's tool registry admits the intersection of `--tools`/`--exclude-tools` and the consumer's scope (pig additive,
// D23: a Piglet's tool scope), so a tool the scope hides is not listed, defined, active or reachable after a refresh.
func TestToolScopeRemovesToolsFromTheRegistry(t *testing.T) {
	keepA := []string{"mcp__docs__a"}
	scope := &extension.ToolScope{Extensions: map[string]*[]string{extension.McpAdapterEntry: &keepA}}
	session := newRegistryPortSession(t, nil, SessionOptions{ToolScope: scope}, nil, scopeProbeTools)
	bindRegistryPort(t, session)
	all := allRegistryNames(session)
	if slices.Contains(all, "mcp__docs__b") || !slices.Contains(all, "mcp__docs__a") || !slices.Contains(all, "dynamic_tool") {
		t.Fatalf("registry = %v, want the scope to remove only mcp__docs__b", all)
	}
	if _, ok := session.GetToolDefinition("mcp__docs__b"); ok {
		t.Error("the hidden tool has a definition")
	}
	// Activation cannot bring a hidden tool back, and a refresh after a late registration keeps it out.
	session.SetActiveToolsByName([]string{"read", "mcp__docs__b", "mcp__docs__a"})
	if err := session.RefreshTools(); err != nil {
		t.Fatal(err)
	}
	if active := session.ActiveToolNames(); slices.Contains(active, "mcp__docs__b") {
		t.Errorf("active = %v, want no hidden tool", active)
	}
	if allRegistryNames(session); slices.Contains(allRegistryNames(session), "mcp__docs__b") {
		t.Error("a refresh readmitted the hidden tool")
	}
}

// The scope sees the namespace and source of a tool (here: only the MCP tools of the adapter entry), and composes with `--tools` and `--exclude-tools` as an intersection.
func TestToolScopeIntersectsWithToolsFlags(t *testing.T) {
	keep := []string{"mcp__docs__a"}
	scope := &extension.ToolScope{Extensions: map[string]*[]string{extension.McpAdapterEntry: &keep}}
	session := newRegistryPortSession(t, nil, SessionOptions{
		ToolScope:     scope,
		AllowedTools:  map[string]struct{}{"mcp__docs__a": {}, "mcp__docs__b": {}, "dynamic_tool": {}},
		ExcludedTools: map[string]struct{}{"dynamic_tool": {}},
	}, nil, scopeProbeTools)
	bindRegistryPort(t, session)
	if all := allRegistryNames(session); !slices.Equal(all, []string{"mcp__docs__a"}) {
		t.Fatalf("registry = %v, want only the tool the scope, --tools and --exclude-tools all admit (the scope drops b, --exclude-tools drops dynamic_tool)", all)
	}
}

// Without a scope the registry is what `--tools` and `--exclude-tools` leave, and a scope that admits everything changes
// nothing: stock PiG without a Piglet is unchanged.
func TestNilToolScopeLeavesTheRegistryUnchanged(t *testing.T) {
	for _, base := range []SessionOptions{
		{},
		{AllowedTools: map[string]struct{}{"read": {}, "mcp__docs__a": {}}},
		{ExcludedTools: map[string]struct{}{"read": {}, "mcp__docs__b": {}}},
		{NoTools: "all"},
	} {
		stock := newRegistryPortSession(t, nil, base, nil, scopeProbeTools)
		bindRegistryPort(t, stock)
		scoped := base
		scoped.ToolScope = &extension.ToolScope{}
		admitting := newRegistryPortSession(t, nil, scoped, nil, scopeProbeTools)
		bindRegistryPort(t, admitting)
		if a, b := allRegistryNames(stock), allRegistryNames(admitting); !slices.Equal(a, b) {
			t.Errorf("registry %v with a scope that admits all, %v without", b, a)
		}
		if a, b := stock.ActiveToolNames(), admitting.ActiveToolNames(); !slices.Equal(a, b) {
			t.Errorf("active %v with a scope that admits all, %v without", b, a)
		}
	}
}
