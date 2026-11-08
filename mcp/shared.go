package mcp

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/mcpshared"
)

// Ports packages/mcp/src/naming.ts and config.ts (Pi 76c040a2a). The implementations live in internal/mcpshared, which coding/extension imports
// directly: it must not link this package, which a pig_strip_mcp build leaves out. Go mechanic: the re-exports keep Pi's pi-mcp exports in this package.

// McpServerConfigKind tags a validated entry with its transport, or marks it invalid.
type McpServerConfigKind = mcpshared.McpServerConfigKind

// Kinds of [McpServerConfigResult].
const (
	McpServerConfigStdio   = mcpshared.McpServerConfigStdio
	McpServerConfigHTTP    = mcpshared.McpServerConfigHTTP
	McpServerConfigInvalid = mcpshared.McpServerConfigInvalid
)

// McpServerConfigResult is a validated server entry, tagged with its transport, or why it is invalid.
type McpServerConfigResult = mcpshared.McpServerConfigResult

// McpServerConfigOptions is what the application adds to the validation of server entries.
type McpServerConfigOptions = mcpshared.McpServerConfigOptions

// McpEntry is an entry of the `mcpServers` shape as an object with named members.
type McpEntry = mcpshared.McpEntry

// ValidateMcpServerConfig validates one entry of the `mcpServers` shape; see [mcpshared.ValidateMcpServerConfig].
func ValidateMcpServerConfig(name string, raw json.RawMessage, options McpServerConfigOptions) McpServerConfigResult {
	return mcpshared.ValidateMcpServerConfig(name, raw, options)
}

// IsMcpServerOverride reports whether an entry overrides a server defined elsewhere instead of defining one.
func IsMcpServerOverride(entry McpEntry) bool { return mcpshared.IsMcpServerOverride(entry) }

// CreateMcpToolName is `mcp__<server>__<tool>`, sanitized and shortened with a hash suffix when too long; see [mcpshared.CreateMcpToolName].
func CreateMcpToolName(server, tool string, isTaken func(string) bool) string {
	return mcpshared.CreateMcpToolName(server, tool, isTaken)
}

// McpNamespace is the namespace of a server's tools: `mcp__<server>` with `-` replaced by `_`.
func McpNamespace(server string) string { return mcpshared.McpNamespace(server) }
