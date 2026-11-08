package mcp

import "github.com/MichaelKinsy/PiG/internal/mcpshared"

// Ports packages/mcp/src/naming.ts (Pi 76c040a2a). The implementation lives in internal/mcpshared (see config.go).

// CreateMcpToolName is `mcp__<server>__<tool>`, sanitized and shortened with a hash suffix when too long; see [mcpshared.CreateMcpToolName].
func CreateMcpToolName(server, tool string, isTaken func(string) bool) string {
	return mcpshared.CreateMcpToolName(server, tool, isTaken)
}

// McpNamespace is the namespace of a server's tools: `mcp__<server>` with `-` replaced by `_`.
func McpNamespace(server string) string { return mcpshared.McpNamespace(server) }
