package extension

import "strings"

// Ports packages/coding-agent/src/core/mcp-servers.ts (createToolNameMatcher, isMcpToolName) and the `--tools` and
// `--exclude-tools` predicates of packages/coding-agent/src/core/agent-session.ts (_isAllowedTool, _isActivatable).

// The MCP resource tools reach every server with resources.
//
// Ports packages/coding-agent/src/core/mcp-servers.ts (LIST_MCP_RESOURCES_TOOL, LIST_MCP_RESOURCE_TEMPLATES_TOOL,
// READ_MCP_RESOURCE_TOOL).
const (
	ListMcpResourcesTool         = "list_mcp_resources"
	ListMcpResourceTemplatesTool = "list_mcp_resource_templates"
	ReadMcpResourceTool          = "read_mcp_resource"
)

// IsMcpToolName reports whether a tool comes from MCP: a server tool (`mcp__<server>__<tool>`) or a resource tool.
//
// Ports packages/coding-agent/src/core/mcp-servers.ts (isMcpToolName).
func IsMcpToolName(name string) bool {
	return strings.HasPrefix(name, "mcp__") || name == ListMcpResourcesTool || name == ListMcpResourceTemplatesTool || name == ReadMcpResourceTool
}

// ToolNameMatcher returns a predicate for tool names that match any entry, each an exact name or a pattern where `*`
// matches any characters. A nil entries set has no matcher: the result is nil.
//
// Ports packages/coding-agent/src/core/mcp-servers.ts (createToolNameMatcher).
func ToolNameMatcher(entries map[string]struct{}) func(name string) bool {
	if entries == nil {
		return nil
	}
	names := make(map[string]struct{}, len(entries))
	var patterns []func(string) bool
	for entry := range entries {
		if !strings.Contains(entry, "*") {
			names[entry] = struct{}{}
			continue
		}
		expression := toolPatternRegExp(entry)
		patterns = append(patterns, expression.MatchString)
	}
	return func(name string) bool {
		if _, ok := names[name]; ok {
			return true
		}
		for _, match := range patterns {
			if match(name) {
				return true
			}
		}
		return false
	}
}

// ToolFilter is the `--tools` allowlist and `--exclude-tools` denylist of a Session. Entries are tool names or `*`
// patterns. A non-empty allowlist without `mcp__` entries keeps MCP tools registered for codemode and tool_search; only
// tool_search can declare them. An empty allowlist (`--no-tools`) removes them.
type ToolFilter struct {
	allowed  func(string) bool
	excluded func(string) bool
	// filtersMcp is whether the allowlist filters MCP tools: it is empty or names an MCP tool (`mcp__*`).
	filtersMcp bool
}

// NewToolFilter compiles an allowlist (nil: none) and a denylist (nil or empty: none).
//
// Ports packages/coding-agent/src/core/agent-session.ts (the constructor's _allowedTools, _allowlistFiltersMcp and
// _excludedTools).
func NewToolFilter(allowed, excluded map[string]struct{}) ToolFilter {
	filter := ToolFilter{allowed: ToolNameMatcher(allowed)}
	if len(excluded) > 0 {
		filter.excluded = ToolNameMatcher(excluded)
	}
	if allowed != nil {
		filter.filtersMcp = len(allowed) == 0
		for entry := range allowed {
			if strings.HasPrefix(entry, "mcp__") {
				filter.filtersMcp = true
			}
		}
	}
	return filter
}

// HasAllowlist reports whether `--tools` or `--no-tools` is in force.
func (f ToolFilter) HasAllowlist() bool { return f.allowed != nil }

// Names reports whether the allowlist names or matches the tool. It is false without an allowlist.
func (f ToolFilter) Names(name string) bool { return f.allowed != nil && f.allowed(name) }

// Allows reports whether `--tools` and `--exclude-tools` keep the tool registered. MCP tools stay registered unless the
// allowlist filters them.
//
// Ports packages/coding-agent/src/core/agent-session.ts (_isAllowedTool).
func (f ToolFilter) Allows(name string) bool {
	if f.excluded != nil && f.excluded(name) {
		return false
	}
	if f.allowed == nil || f.allowed(name) {
		return true
	}
	return !f.filtersMcp && IsMcpToolName(name)
}

// MayBeActive reports whether the tool may be active, which declares it to the model. MCP tools the allowlist keeps
// without matching them are only for codemode and tool_search: they may be declared only when tool_search can load them
// (non-`direct` exposure and tool_search registered). This also applies to tools restored from the transcript or set by
// extensions.
//
// Ports packages/coding-agent/src/core/agent-session.ts (_isActivatable).
func (f ToolFilter) MayBeActive(name string, exposure ToolExposure, toolSearchRegistered bool) bool {
	if f.allowed == nil || f.allowed(name) || !IsMcpToolName(name) {
		return true
	}
	return exposure != ToolExposureDirect && toolSearchRegistered
}
