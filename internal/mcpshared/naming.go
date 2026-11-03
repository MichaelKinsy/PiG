package mcpshared

import "strings"

// Ports packages/mcp/src/naming.ts (mcpNamespace).
//
// The core names the namespace of a server's tools without linking the MCP
// client: a `pig_strip_mcp` build leaves package mcp out, which exposes this
// as [github.com/MichaelKinsy/PiG/mcp.McpNamespace].

// McpNamespace is the namespace of a server's tools: `mcp__<server>` with `-`
// replaced by `_`, like the tool names. Server names that differ only in `-`
// and `_` share a namespace, so a configuration must not have both.
func McpNamespace(server string) string {
	return "mcp__" + strings.ReplaceAll(server, "-", "_")
}
