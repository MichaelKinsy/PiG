//go:build pig_strip_mcp

package codingagent

// offerRadiusMcpServer offers nothing in a build without built-in MCP: no MCP extension reads mcp.json, so a Radius sign-in
// has no MCP server to configure.
// pig additive (D92): a Piglet Binary compiled without MCP skips Pi's Radius MCP offer.
func (m *InteractiveMode) offerRadiusMcpServer(string, string) {}
