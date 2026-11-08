package mcp

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/mcpshared"
)

// Ports packages/mcp/src/config.ts (Pi 76c040a2a). The implementation lives in internal/mcpshared, because coding/extension validates server
// entries and names namespaces in a pig_strip_mcp build, which links neither this package nor mcp/oauth.

// Aliases of the validation types in internal/mcpshared.
type (
	McpServerConfigKind    = mcpshared.McpServerConfigKind
	McpServerConfigResult  = mcpshared.McpServerConfigResult
	McpServerConfigOptions = mcpshared.McpServerConfigOptions
	McpEntry               = mcpshared.McpEntry
)

// Kinds of [McpServerConfigResult].
const (
	McpServerConfigStdio   = mcpshared.McpServerConfigStdio
	McpServerConfigHTTP    = mcpshared.McpServerConfigHTTP
	McpServerConfigInvalid = mcpshared.McpServerConfigInvalid
)

// ValidateMcpServerConfig validates one `mcpServers` entry; see [mcpshared.ValidateMcpServerConfig].
func ValidateMcpServerConfig(name string, raw json.RawMessage, options McpServerConfigOptions) McpServerConfigResult {
	return mcpshared.ValidateMcpServerConfig(name, raw, options)
}

// IsMcpServerOverride reports whether an entry has no `command`, `url` or `type`.
func IsMcpServerOverride(entry McpEntry) bool { return mcpshared.IsMcpServerOverride(entry) }
