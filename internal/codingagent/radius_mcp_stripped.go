//go:build pig_strip_mcp

package codingagent

// RadiusMcpServerPlan is the change to the global mcp.json that points the Radius MCP server at a Radius login.
type RadiusMcpServerPlan struct {
	McpPath string
	Name    string
	Config  []byte
}

// PlanRadiusMcpServer plans nothing in a build without built-in MCP: no MCP extension reads mcp.json, so a Radius sign-in has no MCP server to configure.
// pig additive (D92): a Piglet Binary compiled without MCP skips Pi's Radius MCP configuration.
func PlanRadiusMcpServer(string, string, string) (*RadiusMcpServerPlan, error) { return nil, nil }

// Apply writes nothing: no plan exists in a build without built-in MCP.
func (*RadiusMcpServerPlan) Apply() (bool, error) { return false, nil }
