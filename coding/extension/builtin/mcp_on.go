//go:build !pig_strip_mcp

package builtin

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// McpOptions configures the MCP extension.
type McpOptions = mcpext.Options

func mcpEntries(options Options) []Extension {
	return []Extension{{Name: "mcp", Replaceable: true, Factory: func() (extension.Extension, error) { return mcpext.NewBuiltin(options.Mcp) }}}
}

// ConfigureMcp sets where the MCP extension reads `mcp.json` and keeps its credentials and log (the agent directory and the
// per-project configuration directory name), how it opens an OAuth authorization URL, and, when given, how it copies one.
func (o *Options) ConfigureMcp(agentDir, configDirName string, openURL func(url string), copyToClipboard ...func(text string) error) {
	o.Mcp.AgentDir, o.Mcp.ConfigDirName, o.Mcp.OpenURL = agentDir, configDirName, openURL
	if len(copyToClipboard) > 0 {
		o.Mcp.CopyToClipboard = copyToClipboard[0]
	}
}
