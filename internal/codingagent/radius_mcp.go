//go:build !pig_strip_mcp

package codingagent

// Ports packages/coding-agent/src/core/radius-mcp.ts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/mcpshared"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// RadiusMcpURL is the MCP endpoint of the gateway the built-in Radius provider signs in to (core/radius.ts:6).
var RadiusMcpURL = ai.NormalizeRadiusGatewayURL(ai.DefaultRadiusGateway) + "/mcp"

// RadiusMcpServerPlan is the change to the global mcp.json that points the Radius MCP server at a Radius login.
type RadiusMcpServerPlan struct {
	McpPath string
	Name    string
	Config  json.RawMessage
}

// PlanRadiusMcpServer is the global mcp.json entry that makes the Radius MCP server use the login of providerID, adding the server when missing, or nil
// when a global server already uses that login.
// pig additive (D92): a runtime stripped of mcp plans nothing, like a build without it (radius_mcp_stripped.go).
func PlanRadiusMcpServer(providerID, agentDir, cwd string) (*RadiusMcpServerPlan, error) {
	if pigstrip.Has(pigstrip.ListExtensions, "mcp") {
		return nil, nil
	}
	normalizeURL := func(url string) string { return strings.TrimRight(url, "/") }
	servers := mcpext.LoadMcpConfig(mcpext.LoadOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: false}).Servers
	index := slices.IndexFunc(servers, func(server mcpext.McpServerEntry) bool {
		return server.Config.IsHTTP() && normalizeURL(server.Config.URL) == normalizeURL(RadiusMcpURL)
	})
	if index >= 0 && servers[index].Config.Auth != nil && servers[index].Config.Auth.Provider == providerID {
		return nil, nil
	}

	name := "radius"
	if index >= 0 {
		name = servers[index].Name
	} else {
		namespaces := make(map[string]bool, len(servers))
		for _, server := range servers {
			namespaces[mcpshared.McpNamespace(server.Name)] = true
		}
		for suffix := 1; namespaces[mcpshared.McpNamespace(name)]; suffix++ {
			if suffix == 1 {
				name = "radius-mcp"
			} else {
				name = "radius-mcp-" + strconv.Itoa(suffix)
			}
		}
	}
	mcpPath := filepath.Join(agentDir, "mcp.json")
	config, err := radiusMcpServerConfig(mcpPath, name, index >= 0, providerID)
	if err != nil {
		return nil, err
	}
	return &RadiusMcpServerPlan{McpPath: mcpPath, Name: name, Config: config}, nil
}

// Apply writes the plan to its mcp.json and reports whether it replaced an existing server (addMcpServerConfig).
func (plan *RadiusMcpServerPlan) Apply() (bool, error) {
	return mcpext.AddMcpServerConfigJSON(plan.McpPath, plan.Name, plan.Config)
}

// radiusMcpServerConfig is the server entry that uses the Radius login: an existing entry keeps its members in order
// with `auth` set, as Pi's `{ ...existing.config, auth }` does, and a new one is the Radius MCP URL. `auth` replaces the
// MCP OAuth sign-in, so `oauth` is removed.
func radiusMcpServerConfig(mcpPath, name string, existing bool, providerID string) (json.RawMessage, error) {
	auth, err := json.Marshal(struct {
		Provider string `json:"provider"`
	}{providerID})
	if err != nil {
		return nil, err
	}
	server := orderedjson.New()
	if existing {
		raw, err := globalMcpServerJSON(mcpPath, name)
		if err != nil {
			return nil, err
		}
		if server, err = orderedjson.Parse(raw); err != nil {
			return nil, err
		}
	} else {
		url, _ := json.Marshal(RadiusMcpURL)
		server.Set("url", url)
	}
	server.Set("auth", auth)
	server.Delete("oauth")
	return server.MarshalJSON()
}

// globalMcpServerJSON is the JSON object of one server in an mcp.json.
func globalMcpServerJSON(mcpPath, name string) (json.RawMessage, error) {
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		return nil, err
	}
	// Pi reads this file through loadMcpConfig, which strips a UTF-8 BOM (config.ts readConfigFile, #11191).
	parsed, err := orderedjson.Parse(text.StripBomBytes(data))
	if err != nil {
		return nil, err
	}
	rawServers, _ := parsed.Get("mcpServers")
	servers, err := orderedjson.Parse(rawServers)
	if err != nil {
		return nil, err
	}
	raw, ok := servers.Get(name)
	if !ok {
		return nil, fmt.Errorf(`%s does not define MCP server "%s"`, mcpPath, name)
	}
	return raw, nil
}
