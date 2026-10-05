//go:build pig_strip_mcp

package builtin

// McpOptions is empty in a build without MCP: `mcp`, `mcp/oauth` and `coding/mcpext` are not linked (docs/design/builtin-mcp.md).
type McpOptions struct{}

func mcpEntries(Options) []Extension { return nil }

// ConfigureMcp does nothing: this build has no MCP.
func (o *Options) ConfigureMcp(string, string, func(string), ...func(string) error) {}
