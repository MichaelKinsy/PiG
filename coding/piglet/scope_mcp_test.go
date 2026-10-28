package piglet

import (
	"slices"
	"testing"
)

// A built-in MCP tool (namespace mcp__<server>) is scoped like the adapter's `mcp:<server>` source, and the tools that
// reach servers follow the servers they expose (additive feature D23, docs/design/builtin-mcp.md step 2).
func TestScopeToolsScopesBuiltinMCPNamespaceLikeMCPSource(t *testing.T) {
	allow := []string{"mcp__docs__search"}
	none := []string{}
	all := []ToolInfo{
		{Name: "read", Source: "builtin"},
		{Name: "mcp__docs__search", Source: "mcp", Namespace: "mcp__docs"},
		{Name: "mcp__docs__write", Source: "mcp", Namespace: "mcp__docs"},
		{Name: "mcp__web__fetch", Source: "mcp", Namespace: "mcp__web"},
		{Name: "list_mcp_resources", Source: "mcp"},
		{Name: "read_mcp_resource", Source: "mcp"},
		{Name: "codemode", Source: "codemode"},
		{Name: "tool_search", Source: "tool_search"},
		{Name: "ambient", Source: "other"},
	}
	for _, test := range []struct {
		name   string
		piglet *Piglet
		want   []string
	}{
		{"no extensions section keeps every MCP tool", &Piglet{}, []string{"read", "mcp__docs__search", "mcp__docs__write", "mcp__web__fetch", "list_mcp_resources", "read_mcp_resource", "codemode", "tool_search", "ambient"}},
		{"a positive-only Piglet that omits the adapter hides MCP tools and the tools that reach them", &Piglet{Extensions: []ExtensionEntry{{Name: "other"}}}, []string{"read", "ambient"}},
		{"the adapter entry without tools keeps every server", &Piglet{Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName}}}, []string{"read", "mcp__docs__search", "mcp__docs__write", "mcp__web__fetch", "list_mcp_resources", "read_mcp_resource", "codemode", "tool_search", "ambient"}},
		{"the adapter entry's tools list is a positive entry", &Piglet{Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName, Tools: &allow}}}, []string{"read", "mcp__docs__search", "list_mcp_resources", "read_mcp_resource", "codemode", "tool_search", "ambient"}},
		{"an empty tools list hides every server and what reaches them", &Piglet{Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName, Tools: &none}}}, []string{"read", "ambient"}},
		{"selecting builtin:codemode keeps codemode when no server is visible", &Piglet{Extensions: []ExtensionEntry{{Name: "cm", Origins: []string{"builtin:codemode"}}}}, []string{"read", "codemode", "ambient"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ScopeTools(test.piglet, all); !slices.Equal(got, test.want) {
				t.Fatalf("ScopeTools = %v, want %v", got, test.want)
			}
		})
	}
}

// Without any MCP tool registered, codemode and tool_search are not tools of a hidden server and stay.
func TestScopeToolsKeepsCodemodeWhenNoMCPToolIsRegistered(t *testing.T) {
	piglet := &Piglet{Extensions: []ExtensionEntry{{Name: "other"}}}
	all := []ToolInfo{{Name: "read", Source: "builtin"}, {Name: "codemode", Source: "codemode"}}
	if got := ScopeTools(piglet, all); !slices.Equal(got, []string{"read", "codemode"}) {
		t.Fatalf("ScopeTools = %v", got)
	}
}
