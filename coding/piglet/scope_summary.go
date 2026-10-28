package piglet

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// mcpScope says what the Piglet does to MCP tools: a short summary and the reason, both "" when it keeps all of them. The
// reasons are the ones of [extension.ToolScope.Why], so they follow the code that scopes the registry.
func mcpScope(p *Piglet) (summary, reason string) {
	if len(p.Extensions) == 0 {
		return "", ""
	}
	adapter := slices.IndexFunc(p.Extensions, func(e ExtensionEntry) bool { return e.Name == MCPAdapterExtensionName })
	switch {
	case adapter < 0:
		return "hidden", "the Piglet lists `extensions:` but not `" + MCPAdapterExtensionName + "`"
	case p.Extensions[adapter].Tools == nil:
		return "", ""
	case len(*p.Extensions[adapter].Tools) == 0:
		return "hidden", "the `" + MCPAdapterExtensionName + "` entry lists no tools"
	}
	return "only " + strings.Join(*p.Extensions[adapter].Tools, ", "), "the `" + MCPAdapterExtensionName + "` entry's `tools` list does not name the others"
}

// MCPScopeLine says in one line what the Piglet does to MCP tools and why, or "" when it keeps all of them. It is the
// static counterpart of [ToolScopeNotice]: it needs no running MCP server.
func MCPScopeLine(p *Piglet) string {
	summary, reason := mcpScope(p)
	if summary == "" {
		return ""
	}
	return "MCP tools: " + summary + ", because " + reason
}

// ToolScopeSummary is the one-line tool scope of a Piglet for the agent's context: the built-in tools it keeps, the MCP
// tools it keeps and the extensions whose tools it narrows.
func ToolScopeSummary(p *Piglet) string {
	var parts []string
	switch {
	case p.BuiltinTools == nil:
		parts = append(parts, "all built-in tools")
	case len(*p.BuiltinTools) == 0:
		parts = append(parts, "no built-in tools")
	default:
		parts = append(parts, "built-in tools "+strings.Join(*p.BuiltinTools, ", "))
	}
	if summary, _ := mcpScope(p); summary == "" {
		parts = append(parts, "all MCP tools")
	} else {
		parts = append(parts, "MCP tools "+summary)
	}
	for _, entry := range p.Extensions {
		if entry.Name == MCPAdapterExtensionName || entry.Tools == nil {
			continue
		}
		if len(*entry.Tools) == 0 {
			parts = append(parts, "no tools of "+entry.Name)
			continue
		}
		parts = append(parts, "tools of "+entry.Name+" only "+strings.Join(*entry.Tools, ", "))
	}
	return strings.Join(parts, "; ")
}

// AgentToolScopeLine is the line the agent gets when a Piglet is active, so it knows what it can and cannot reach.
func AgentToolScopeLine(p *Piglet) string {
	return fmt.Sprintf("Active Piglet: %s. Tool scope: %s.", p.Name, ToolScopeSummary(p))
}

// ToolScopeNotice lists the tools the Piglet's scope removed from the registry, grouped by MCP server or owning
// extension, each group with its reason. It is "" when the scope removed nothing.
func ToolScopeNotice(p *Piglet, hidden []extension.HiddenTool) string {
	if len(hidden) == 0 {
		return ""
	}
	type group struct{ subject, why string }
	var order []group
	names := map[group][]string{}
	for _, tool := range hidden {
		subject := "built-in tools"
		switch {
		case tool.Server != "":
			subject = "MCP server " + tool.Server
		case tool.Owner == MCPAdapterExtensionName:
			subject = "MCP resource tools"
		case tool.Owner != "":
			subject = "extension " + tool.Owner
		}
		key := group{subject, tool.Why}
		if _, seen := names[key]; !seen {
			order = append(order, key)
		}
		names[key] = append(names[key], tool.Name)
	}
	lines := []string{fmt.Sprintf("Piglet %s hid %d tools from the registry:", p.Name, len(hidden))}
	for _, key := range order {
		lines = append(lines, fmt.Sprintf("- %s: %s (%s)", key.subject, strings.Join(names[key], ", "), key.why))
	}
	return strings.Join(lines, "\n")
}
