package piglet

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// MCPAdapterExtensionName is the Piglet extension entry that governs every MCP tool.
const MCPAdapterExtensionName = extension.McpAdapterEntry

// ToolInfo is the minimal metadata needed for Piglet tool scoping.
type ToolInfo struct {
	Name   string
	Source string
	// Namespace is the tool's namespace name, `mcp__<server>` for a built-in MCP tool (extension.McpNamespace). Empty when the tool has none.
	Namespace string
}

// CompileToolScope compiles a Piglet's tool scope into the form the Session's registry filter reads
// (extension.ToolFilter.WithScope; pig additive, D23). owner names the Piglet extension entry that registered an
// extension tool, or "" when no loaded extension did. The rules are documented on [extension.ToolScope].
func CompileToolScope(piglet *Piglet, owner func(extension.ToolInfo) string) *extension.ToolScope {
	scope := &extension.ToolScope{Builtin: piglet.BuiltinTools, Extensions: make(map[string]*[]string, len(piglet.Extensions)), Owner: owner}
	for i := range piglet.Extensions {
		scope.Extensions[piglet.Extensions[i].Name] = piglet.Extensions[i].Tools
	}
	return scope
}

// ScopeTools returns the names of the registered tools the Piglet keeps, in registry order.
func ScopeTools(piglet *Piglet, allTools []ToolInfo) []string {
	scope := CompileToolScope(piglet, nil)
	active := make([]string, 0, len(allTools))
	for _, tool := range allTools {
		info := extension.ToolInfo{Name: tool.Name, Source: tool.Source}
		if tool.Namespace != "" {
			info.Namespace = &extension.ToolNamespace{Name: tool.Namespace}
		}
		if scope.Keeps(info) {
			active = append(active, tool.Name)
		}
	}
	return active
}
