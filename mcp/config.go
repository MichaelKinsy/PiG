package mcp

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/mcpshared"
)

// Ports packages/mcp/src/config.ts.
//
// The `mcpServers` configuration shape that MCP clients share, so
// configurations can be copied between them: stdio servers (`command`, `args`,
// `env`, `cwd`), Streamable HTTP servers (`url`, `headers`, `oauth`), and
// `enabled`, `description` and `timeout` for both. The implementation is
// internal/mcpshared's, which the core links without the MCP client (a
// `pig_strip_mcp` build leaves package mcp out); this file is the package's API.
//
// Upstream's `McpStdioServerConfig`, `McpHttpServerConfig`, `McpOAuthConfig`
// and `McpServerConfig` types describe the entry for TypeScript callers that
// extend them. Go has no interface extension, and a caller decodes the entry
// into its own struct, so the result carries the entry's JSON instead.

// McpServerConfigKind tags a validated server entry with its transport.
type McpServerConfigKind = mcpshared.McpServerConfigKind

// The kinds of [McpServerConfigResult].
const (
	McpServerConfigStdio   = mcpshared.McpServerConfigStdio
	McpServerConfigHTTP    = mcpshared.McpServerConfigHTTP
	McpServerConfigInvalid = mcpshared.McpServerConfigInvalid
)

// McpServerConfigResult is a validated server entry, tagged with its
// transport, or why it is invalid. Config is the entry as given: members the
// validator does not know are kept, unchecked, for the caller to check. It is
// empty for an invalid entry, and Error is empty for a valid one.
type McpServerConfigResult = mcpshared.McpServerConfigResult

// McpServerConfigOptions is what the application adds to the validation of
// server entries.
type McpServerConfigOptions = mcpshared.McpServerConfigOptions

// ValidateMcpServerConfig validates one entry of the `mcpServers` shape
// (configuration files are a trust boundary). Server names are letters, digits,
// `_` and `-`, which tool names and namespaces are built from.
func ValidateMcpServerConfig(name string, raw json.RawMessage, options McpServerConfigOptions) McpServerConfigResult {
	return mcpshared.ValidateMcpServerConfig(name, raw, options)
}

// IsMcpServerOverride reports whether an entry (its members) overrides a
// server defined elsewhere instead of defining one: it has no `command`, `url`
// or `type`, and a member that is present counts whatever its value.
func IsMcpServerOverride(entry map[string]json.RawMessage) bool {
	return mcpshared.IsMcpServerOverride(entry)
}
