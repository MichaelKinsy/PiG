package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (offerRadiusMcpServer).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/tui"
)

// RadiusMcpURL is the MCP endpoint of the gateway the built-in Radius provider signs in to (core/radius.ts:6).
var RadiusMcpURL = ai.NormalizeRadiusGatewayURL(ai.DefaultRadiusGateway) + "/mcp"

// offerRadiusMcpServer offers to point the Radius MCP server in the global mcp.json at the Radius login, adding the
// server when missing. Nothing is asked when a global server already uses this login. Mirrors Pi's
// offerRadiusMcpServer (interactive-mode.ts:6292-6343).
func (m *InteractiveMode) offerRadiusMcpServer(providerID, providerName string) {
	mcpPath := filepath.Join(m.opts.AgentDir, "mcp.json")
	normalizeURL := func(url string) string { return strings.TrimRight(url, "/") }
	servers := mcpext.LoadMcpConfig(mcpext.LoadOptions{AgentDir: m.opts.AgentDir, Cwd: m.opts.CWD, ProjectTrusted: false}).Servers
	index := slices.IndexFunc(servers, func(server mcpext.McpServerEntry) bool {
		return server.Config.IsHTTP() && normalizeURL(server.Config.URL) == normalizeURL(RadiusMcpURL)
	})
	if index >= 0 && servers[index].Config.Auth != nil && servers[index].Config.Auth.Provider == providerID {
		return
	}

	name := "radius"
	if index >= 0 {
		name = servers[index].Name
	} else if slices.ContainsFunc(servers, func(server mcpext.McpServerEntry) bool { return server.Name == name }) {
		name = "radius-mcp"
	}
	config, err := radiusMcpServerConfig(mcpPath, name, index >= 0, providerID)
	if err != nil {
		m.showError(fmt.Sprintf("Could not update %s: %v", mcpPath, err))
		return
	}

	selector := tui.NewExtensionSelector(fmt.Sprintf("Configure %s MCP in %s?", providerName, mcpPath), []string{"Yes", "No"})
	index, ok := m.runEditorSlotExtensionSelector(selector)
	if !ok || index != 0 {
		return
	}
	if _, err := mcpext.AddMcpServerConfigJSON(mcpPath, name, config); err != nil {
		m.showError(fmt.Sprintf("Could not update %s: %v", mcpPath, err))
		return
	}
	// The MCP extension reads mcp.json when the session starts. Pi runs its /reload handler directly
	// (interactive-mode.ts:6334 handleReloadCommand), not the slash command an extension may override.
	if err := reloadHandler(m.buildSlashContext(m.loginContext())); err != nil {
		m.showError(err.Error())
	}
	m.tuiInst.Render()
}

// radiusMcpServerConfig is the server entry that uses the Radius login: an existing entry keeps its members in order
// with `auth` set, as Pi's `{ ...existing.config, auth }` does, and a new one is the Radius MCP URL. `auth` replaces the
// MCP OAuth sign-in, so `oauth` is removed.
func radiusMcpServerConfig(mcpPath, name string, existing bool, providerID string) (json.RawMessage, error) {
	auth, err := json.Marshal(map[string]string{"provider": providerID})
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
	parsed, err := orderedjson.Parse(data)
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
