package piglet

import (
	"slices"
	"strings"
)

// MCPSourcePrefix identifies tools registered by an MCP adapter for one server.
const MCPSourcePrefix = "mcp:"

// MCPAdapterExtensionName is the conventional owner of MCP tools.
const MCPAdapterExtensionName = "pig-mcp-adapter"

// ToolInfo is the minimal metadata needed for Piglet tool scoping.
type ToolInfo struct {
	Name   string
	Source string
}

// ScopeTools intersects registered tools with root and extension allowlists.
// Resource loading/discovery decides which extensions exist; this function does
// not use Piglet membership as a second discovery gate.
func ScopeTools(piglet *Piglet, allTools []ToolInfo) []string {
	extensionTools := make(map[string]*[]string, len(piglet.Extensions))
	for i := range piglet.Extensions {
		extensionTools[piglet.Extensions[i].Name] = piglet.Extensions[i].Tools
	}

	active := make([]string, 0, len(allTools))
	for _, tool := range allTools {
		source := tool.Source
		switch {
		case source == "" || source == "builtin":
			if toolAllowed(piglet.BuiltinTools, tool.Name) {
				active = append(active, tool.Name)
			}
		case strings.HasPrefix(source, MCPSourcePrefix):
			if toolAllowed(extensionTools[MCPAdapterExtensionName], tool.Name) {
				active = append(active, tool.Name)
			}
		default:
			if toolAllowed(extensionTools[source], tool.Name) {
				active = append(active, tool.Name)
			}
		}
	}
	return active
}

func toolAllowed(allowlist *[]string, name string) bool {
	return allowlist == nil || slices.Contains(*allowlist, name)
}
