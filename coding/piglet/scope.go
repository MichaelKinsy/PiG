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

// Scope is a Piglet's tool scope compiled for per-tool decisions.
type Scope struct {
	piglet         *Piglet
	extensionTools map[string]*[]string
}

// NewScope compiles the tool scope of a Piglet.
func NewScope(piglet *Piglet) *Scope {
	extensionTools := make(map[string]*[]string, len(piglet.Extensions))
	for i := range piglet.Extensions {
		extensionTools[piglet.Extensions[i].Name] = piglet.Extensions[i].Tools
	}
	return &Scope{piglet: piglet, extensionTools: extensionTools}
}

// Visible reports whether the Piglet keeps one tool. Resource loading/discovery decides which extensions exist; this
// does not use Piglet membership as a second discovery gate, except for MCP tools.
//
// An MCP tool is a tool whose source has the `mcp:` prefix (the pig-mcp-adapter path), whose namespace is
// `mcp__<server>` (the built-in MCP extension), or that is one of the MCP resource tools. All are governed by the
// `pig-mcp-adapter` extension entry: a Piglet with no `extensions:` section keeps them, a positive-only Piglet that does
// not list the entry hides them, and a listed entry applies its `tools` allowlist. Any other extension's tool is governed
// by that extension's entry, and a built-in tool by the root `tools` list.
func (s *Scope) Visible(tool ToolInfo) bool {
	switch {
	case isMCPTool(tool):
		return mcpAllowed(s.piglet, s.extensionTools, tool.Name)
	case tool.Source == "" || tool.Source == "builtin":
		return toolAllowed(s.piglet.BuiltinTools, tool.Name)
	default:
		return toolAllowed(s.extensionTools[tool.Source], tool.Name)
	}
}

// ScopeTools returns the names of the registered tools the Piglet keeps, in registry order.
func ScopeTools(piglet *Piglet, allTools []ToolInfo) []string {
	scope := NewScope(piglet)
	active := make([]string, 0, len(allTools))
	for _, tool := range allTools {
		if scope.Visible(tool) {
			active = append(active, tool.Name)
		}
	}
	return active
}

// isMCPTool reports whether the tool belongs to an MCP server or reaches its resources: the adapter's `mcp:<server>`
// source, the built-in extension's `mcp__<server>` namespace, or an MCP resource tool.
func isMCPTool(tool ToolInfo) bool {
	switch tool.Name {
	case extension.ListMcpResourcesTool, extension.ListMcpResourceTemplatesTool, extension.ReadMcpResourceTool:
		return true
	}
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

func toolAllowed(allowlist *[]string, name string) bool {
	return allowlist == nil || slices.Contains(*allowlist, name)
}
