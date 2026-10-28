package piglet

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
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

// ToolScopeSummary is the one-line tool scope of a Piglet for the agent's context. It states the most the agent can
// reach: the root `tools` list is a ceiling over the default active tools, and the strip lists remove tools in this
// process, so the line leaves out stripped built-in tools and says no MCP tools when MCP is stripped. The agent line says
// "at most" for that reason.
func ToolScopeSummary(p *Piglet) string {
	var parts []string
	stripped := slices.DeleteFunc(pigstrip.Known(pigstrip.ListTools), func(name string) bool { return !pigstrip.Has(pigstrip.ListTools, name) })
	switch {
	case p.BuiltinTools == nil && len(stripped) == 0:
		parts = append(parts, "all built-in tools")
	case p.BuiltinTools == nil:
		parts = append(parts, "all built-in tools except "+strings.Join(stripped, ", "))
	default:
		kept := slices.DeleteFunc(slices.Clone(*p.BuiltinTools), func(name string) bool { return slices.Contains(stripped, name) })
		if len(kept) == 0 {
			parts = append(parts, "no built-in tools")
		} else {
			parts = append(parts, "built-in tools "+strings.Join(kept, ", "))
		}
	}
	switch summary, _ := mcpScope(p); {
	case pigstrip.Has(pigstrip.ListExtensions, "mcp"):
		parts = append(parts, "no MCP tools")
	case summary == "":
		parts = append(parts, "all MCP tools")
	default:
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

// AgentToolScopeLine is the line the agent gets when a Piglet is active, so it knows what it can and cannot reach: the
// Piglet (and the Piglet Binary it overlays), the most its tool scope lets it reach, what the strip lists removed, and
// where to read the details. A Piglet Binary has no docs section, so this line is all its agent learns.
func AgentToolScopeLine(p *Piglet) string {
	var line strings.Builder
	line.WriteString("Active Piglet: " + p.Name)
	if len(p.lineage) > 0 {
		if binary, ok := strings.CutPrefix(p.lineage[0].Source, "binary:"); ok {
			line.WriteString(" (overlay " + p.SourcePath() + " on Piglet Binary " + binary + ")")
		}
	}
	line.WriteString(". Tool scope, at most: " + ToolScopeSummary(p) + ".")
	var stripped []string
	for _, list := range pigstrip.Lists() {
		if ids := pigstrip.IDs(list); len(ids) > 0 {
			stripped = append(stripped, list+" "+strings.Join(ids, ", "))
		}
	}
	if len(stripped) > 0 {
		line.WriteString(" Stripped: " + strings.Join(stripped, "; ") + ".")
	}
	details := p.SourcePath()
	if details == "" {
		details = p.Name
	}
	line.WriteString(" Details: pig piglet show " + details + ".")
	return line.String()
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
