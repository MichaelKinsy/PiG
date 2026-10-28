package piglet

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// MCPSourcePrefix identifies tools registered by an MCP adapter for one server.
const MCPSourcePrefix = "mcp:"

// MCPAdapterExtensionName is the conventional owner of MCP tools.
const MCPAdapterExtensionName = "pig-mcp-adapter"

// ToolInfo is the minimal metadata needed for Piglet tool scoping.
type ToolInfo struct {
	Name   string
	Source string
	// Namespace is the tool's namespace name, `mcp__<server>` for a built-in MCP tool (extension.McpNamespace). Empty when the tool has none.
	Namespace string
}

// ScopeTools intersects registered tools with root and extension allowlists.
// Resource loading/discovery decides which extensions exist; this function does
// not use Piglet membership as a second discovery gate, except for MCP tools.
//
// An MCP tool is a tool whose source has the `mcp:` prefix (the pig-mcp-adapter
// path) or whose namespace is `mcp__<server>` (the built-in MCP extension). Both
// are governed by the `pig-mcp-adapter` extension entry: a Piglet with no
// `extensions:` section keeps them, a positive-only Piglet that does not list
// the entry hides them, and a listed entry applies its `tools` allowlist.
// The tools that reach MCP servers (codemode, tool_search, and the MCP resource
// tools) follow the servers they expose; see followsMCPServers.
func ScopeTools(piglet *Piglet, allTools []ToolInfo) []string {
	extensionTools := make(map[string]*[]string, len(piglet.Extensions))
	for i := range piglet.Extensions {
		extensionTools[piglet.Extensions[i].Name] = piglet.Extensions[i].Tools
	}

	active := make([]string, 0, len(allTools))
	mcpRegistered, mcpVisible := false, false
	var reaching []string
	for _, tool := range allTools {
		source := tool.Source
		switch {
		case isMCPTool(tool):
			mcpRegistered = true
			if mcpAllowed(piglet, extensionTools, tool.Name) {
				mcpVisible = true
				active = append(active, tool.Name)
			}
		case source == "" || source == "builtin":
			if toolAllowed(piglet.BuiltinTools, tool.Name) {
				active = append(active, tool.Name)
			}
		default:
			if toolAllowed(extensionTools[source], tool.Name) {
				active = append(active, tool.Name)
			}
		}
		if followsMCPServers(piglet, tool) {
			reaching = append(reaching, tool.Name)
		}
	}
	// Tools that reach servers reach nothing once the Piglet hides every server's tools.
	if mcpRegistered && !mcpVisible {
		active = slices.DeleteFunc(active, func(name string) bool { return slices.Contains(reaching, name) })
	}
	return active
}

// isMCPTool reports whether the tool belongs to one MCP server: the adapter's `mcp:<server>` source or the built-in
// extension's `mcp__<server>` namespace.
func isMCPTool(tool ToolInfo) bool {
	return strings.HasPrefix(tool.Source, MCPSourcePrefix) || strings.HasPrefix(tool.Namespace, mcpNamespacePrefix)
}

// mcpNamespacePrefix is the start of every built-in MCP server namespace (extension.McpNamespace).
var mcpNamespacePrefix = extension.McpNamespace("")

// mcpAllowed applies the `pig-mcp-adapter` entry. A Piglet with extensions but no such entry is positive-only for MCP:
// it hides the tools of every server it does not name.
func mcpAllowed(piglet *Piglet, extensionTools map[string]*[]string, name string) bool {
	if len(piglet.Extensions) == 0 {
		return true
	}
	allowlist, listed := extensionTools[MCPAdapterExtensionName]
	return listed && toolAllowed(allowlist, name)
}

// followsMCPServers reports whether the tool exists to reach MCP servers: the MCP resource tools, and codemode and
// tool_search, which the built-in MCP extension activates for servers whose tools are not declared. A Piglet that
// selects builtin:codemode or builtin:tool-search itself keeps that tool, since it then serves the Piglet's other tools.
func followsMCPServers(piglet *Piglet, tool ToolInfo) bool {
	switch tool.Name {
	case extension.ListMcpResourcesTool, extension.ListMcpResourceTemplatesTool, extension.ReadMcpResourceTool:
		return true
	case "codemode":
		return !selectsOrigin(piglet, "builtin:codemode")
	case "tool_search":
		return !selectsOrigin(piglet, "builtin:tool-search")
	}
	return false
}

func selectsOrigin(piglet *Piglet, origin string) bool {
	return slices.ContainsFunc(piglet.Extensions, func(e ExtensionEntry) bool { return slices.Contains(e.Origins, origin) })
}

func toolAllowed(allowlist *[]string, name string) bool {
	return allowlist == nil || slices.Contains(*allowlist, name)
}
