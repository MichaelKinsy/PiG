//go:build !pig_strip_mcp

package builtin

import (
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// McpOptions configures the MCP extension.
type McpOptions = mcpext.Options

// mcpEntries is the mcp registry row, absent while MCP is stripped at runtime as in a build without it.
// pig additive (D92): a runtime stripped mcp registers nothing, like the pig_strip_mcp build.
func mcpEntries(options Options) []Extension {
	if pigstrip.Has(pigstrip.ListExtensions, "mcp") {
		return nil
	}
	return []Extension{{Name: "mcp", Replaceable: true, Factory: mcpext.CreateMcpExtension(options.Mcp)}}
}

// ConfigureMcp sets where the MCP extension reads `mcp.json` and keeps its credentials and log (the agent directory and the
// per-project configuration directory name), how it opens an OAuth authorization URL, and, when given, how it copies one.
func (o *Options) ConfigureMcp(agentDir, configDirName string, openURL func(url string), copyToClipboard ...func(text string) error) {
	o.Mcp.AgentDir, o.Mcp.ConfigDirName, o.Mcp.OpenURL = agentDir, configDirName, openURL
	if len(copyToClipboard) > 0 {
		o.Mcp.CopyToClipboard = copyToClipboard[0]
	}
}
