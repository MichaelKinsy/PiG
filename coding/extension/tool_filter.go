package extension

import (
	"slices"
	"strings"
	"sync"
)

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
	return strings.HasPrefix(name, "mcp__") || IsMcpResourceTool(name)
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
	// scope is a Piglet's tool scope; nil places no restriction.
	scope *ToolScope
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

// WithScope returns the filter that also requires a Piglet's tool scope to keep a tool: the registry keeps the
// intersection of `--tools`/`--exclude-tools` and the scope. A nil scope returns the filter unchanged.
//
// pig additive (D23): Pi has no Piglets.
func (f ToolFilter) WithScope(scope *ToolScope) ToolFilter {
	f.scope = scope
	return f
}

// AllowsTool reports whether the registry keeps a tool: [ToolFilter.Allows] for its name, and the scope, when there is
// one, for its source, namespace and name. Without a scope it is exactly Allows.
func (f ToolFilter) AllowsTool(info ToolInfo) bool {
	return f.Allows(info.Name) && (f.scope == nil || f.scope.Keeps(info))
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

// MCP tool classes of a Piglet's tool scope.
const (
	// McpSourcePrefix starts the source of a tool the pig-mcp-adapter registers for one server (`mcp:<server>`).
	McpSourcePrefix = "mcp:"
	// McpAdapterEntry is the Piglet extension entry that governs every MCP tool, whichever extension registers it.
	McpAdapterEntry = "pig-mcp-adapter"
)

// ToolScope is a Piglet's tool scope in the form the registry filter reads (pig additive, D23). Resource loading decides
// which extensions exist, so the scope is not a second discovery gate, except for MCP tools.
//
// A tool is an MCP tool when its source starts with `mcp:` (the adapter), its namespace is `mcp__<server>` (the built-in
// MCP extension), or it is one of the MCP resource tools. The [McpAdapterEntry] entry governs all of them: a Piglet with no
// `extensions:` section keeps them, one that lists extensions but not the entry hides them, and a listed entry applies its
// `tools` list. Any other extension's tool follows that extension's entry; a built-in tool follows the root `tools` list.
// An absent list keeps every tool of its owner.
type ToolScope struct {
	// Builtin is the root `tools` list.
	Builtin *[]string
	// Extensions are the `extensions:` entries by name, each with its `tools` list.
	Extensions map[string]*[]string
	// Owner names the entry of the extension that registered a tool, or "" when none did.
	Owner func(ToolInfo) string

	mu     sync.Mutex
	hidden []HiddenTool
}

// HiddenTool is a registered tool the scope removed from the registry.
type HiddenTool struct {
	Name string
	// Server is the MCP server of the tool, "" for a tool of no server.
	Server string
	// Owner is the extension entry that owns the tool, "" for a built-in tool.
	Owner string
	// Why says in one sentence why the scope hides the tool.
	Why string
}

// SetHidden records the tools the last registry refresh removed by scope, in registry order, for the startup notice.
func (s *ToolScope) SetHidden(tools []HiddenTool) {
	s.mu.Lock()
	s.hidden = tools
	s.mu.Unlock()
}

// Hidden returns the tools the last registry refresh removed by scope.
func (s *ToolScope) Hidden() []HiddenTool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.hidden)
}

// HiddenInfo describes a tool the scope hides; it is the zero value for a tool the scope keeps.
func (s *ToolScope) HiddenInfo(info ToolInfo) (HiddenTool, bool) {
	why := s.Why(info)
	if why == "" {
		return HiddenTool{}, false
	}
	_, owner := s.KindOf(info)
	return HiddenTool{Name: info.Name, Server: s.McpServerOf(info), Owner: owner, Why: why}, true
}

// ToolClass says which rule of the scope decides a tool.
type ToolClass string

// The classes of tools a scope tells apart.
const (
	ToolClassBuiltin   ToolClass = "built-in"
	ToolClassMCP       ToolClass = "mcp"
	ToolClassExtension ToolClass = "extension"
)

// KindOf names the class and the owner entry of a tool. The owner of a built-in tool is "".
func (s *ToolScope) KindOf(info ToolInfo) (ToolClass, string) {
	source, namespace := s.source(info), ""
	if info.Namespace != nil {
		namespace = info.Namespace.Name
	}
	switch {
	case IsMcpResourceTool(info.Name) || strings.HasPrefix(source, McpSourcePrefix) || strings.HasPrefix(namespace, McpNamespace("")):
		return ToolClassMCP, McpAdapterEntry
	case source == "" || source == "builtin":
		return ToolClassBuiltin, ""
	}
	return ToolClassExtension, source
}

// source is the entry that owns the tool: its declared source, the registering extension's entry, or, for a tool whose
// provenance names no entry, the provenance's own source. A tool with none of these is a built-in.
func (s *ToolScope) source(info ToolInfo) string {
	source := info.Source
	if source == "" && s.Owner != nil {
		source = s.Owner(info)
	}
	if source == "" {
		if provenance := info.SourceInfo; provenance.Source != "builtin" && (provenance.Source != "" || provenance.Path != "") {
			source = provenance.Source
			if source == "" {
				source = provenance.Path
			}
		}
	}
	return source
}

// Keeps reports whether the scope keeps a tool.
func (s *ToolScope) Keeps(info ToolInfo) bool {
	class, owner := s.KindOf(info)
	return s.keeps(class, owner, info.Name)
}

func (s *ToolScope) keeps(class ToolClass, owner, name string) bool {
	switch class {
	case ToolClassBuiltin:
		return listKeeps(s.Builtin, name)
	case ToolClassMCP:
		if len(s.Extensions) == 0 {
			return true
		}
		list, listed := s.Extensions[owner]
		return listed && listKeeps(list, name)
	}
	return listKeeps(s.Extensions[owner], name)
}

// Why says in one sentence why the scope hides a tool, or "" when it keeps it.
func (s *ToolScope) Why(info ToolInfo) string {
	class, owner := s.KindOf(info)
	if s.keeps(class, owner, info.Name) {
		return ""
	}
	switch class {
	case ToolClassBuiltin:
		return "the Piglet's `tools` list does not name it"
	case ToolClassMCP:
		if _, listed := s.Extensions[owner]; !listed {
			return "the Piglet lists `extensions:` but not `" + McpAdapterEntry + "`"
		}
		return "the `" + McpAdapterEntry + "` entry's `tools` list does not name it"
	}
	return "the `" + owner + "` entry's `tools` list does not name it"
}

func listKeeps(list *[]string, name string) bool {
	return list == nil || slices.Contains(*list, name)
}

// McpServerOf names the MCP server of a tool: the server in `mcp:<server>` or `mcp__<server>`, or "" for an MCP resource
// tool or a tool of no server.
func (s *ToolScope) McpServerOf(info ToolInfo) string {
	if server, ok := strings.CutPrefix(s.source(info), McpSourcePrefix); ok {
		return server
	}
	if info.Namespace != nil {
		return strings.TrimPrefix(info.Namespace.Name, McpNamespace(""))
	}
	return ""
}

// IsMcpResourceTool reports whether the name is one of the MCP resource tools.
func IsMcpResourceTool(name string) bool {
	return name == ListMcpResourcesTool || name == ListMcpResourceTemplatesTool || name == ReadMcpResourceTool
}
