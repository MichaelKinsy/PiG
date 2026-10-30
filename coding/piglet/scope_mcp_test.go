package piglet

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A built-in MCP tool (namespace mcp__<server>) is scoped like the adapter's `mcp:<server>` source, and the MCP resource
// tools are MCP tools the same entry governs; codemode and tool_search are ordinary tools, which reach only what the
// registry keeps (additive feature D23, docs/design/builtin-mcp.md step 2).
func TestScopeToolsScopesBuiltinMCPNamespaceLikeMCPSource(t *testing.T) {
	allow := []string{"mcp__docs__search"}
	none := []string{}
	allowResource := []string{"read_mcp_resource"}
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
		{"a positive-only Piglet that omits the adapter hides MCP tools and the resource tools", &Piglet{Extensions: []ExtensionEntry{{Name: "other"}}}, []string{"read", "codemode", "tool_search", "ambient"}},
		{"the adapter entry without tools keeps every server", &Piglet{Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName}}}, []string{"read", "mcp__docs__search", "mcp__docs__write", "mcp__web__fetch", "list_mcp_resources", "read_mcp_resource", "codemode", "tool_search", "ambient"}},
		{"the adapter entry's tools list is a positive entry", &Piglet{Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName, Tools: &allow}}}, []string{"read", "mcp__docs__search", "codemode", "tool_search", "ambient"}},
		{"the adapter entry's tools list names a resource tool", &Piglet{Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName, Tools: &allowResource}}}, []string{"read", "read_mcp_resource", "codemode", "tool_search", "ambient"}},
		{"an empty tools list hides every server and the resource tools", &Piglet{Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName, Tools: &none}}}, []string{"read", "codemode", "tool_search", "ambient"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ScopeTools(test.piglet, all); !slices.Equal(got, test.want) {
				t.Fatalf("ScopeTools = %v, want %v", got, test.want)
			}
		})
	}
}

// The compiled scope decides per tool from the extension.ToolInfo the Session reports: the namespace and the owning
// extension entry, as ScopeTools does for the names GetAllTools lists.
func TestCompiledToolScopeKeepsToolsPerSourceAndNamespace(t *testing.T) {
	allow := []string{"mcp__docs__a"}
	only := []string{"shown"}
	p := &Piglet{Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName, Tools: &allow}, {Name: "scoped", Tools: &only}}}
	owner := func(tool extension.ToolInfo) string {
		if strings.HasPrefix(tool.Name, "shown") || strings.HasPrefix(tool.Name, "hidden") {
			return "scoped"
		}
		return ""
	}
	compiled := CompileToolScope(p, owner)
	docs := &extension.ToolNamespace{Name: "mcp__docs"}
	for _, test := range []struct {
		tool extension.ToolInfo
		want bool
	}{
		{extension.ToolInfo{Name: "mcp__docs__a", Namespace: docs}, true},
		{extension.ToolInfo{Name: "mcp__docs__b", Namespace: docs}, false},
		{extension.ToolInfo{Name: "shown"}, true},
		{extension.ToolInfo{Name: "hidden"}, false},
		{extension.ToolInfo{Name: "read"}, true},
		{extension.ToolInfo{Name: "codemode"}, true},
		{extension.ToolInfo{Name: "read_mcp_resource"}, false},
	} {
		if got := compiled.Keeps(test.tool); got != test.want {
			t.Errorf("scope(%q) = %t, want %t", test.tool.Name, got, test.want)
		}
	}
}
