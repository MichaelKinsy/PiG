package mcpext

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's isMcpAppResource (extensions/mcp/resources.js) against IsMcpAppResource: a `ui://` URI, or a `; profile=mcp-app` media type with JavaScript `\s` around the tokens and ASCII-only case folding. Pi 1.2.0 no longer exports isMcpAppResource, so the oracle reaches it through the list_mcp_resources tool, which leaves out every resource it matches.
func TestIsMcpAppResourceMatchesPiOracle(t *testing.T) {
	uris := []string{"", "ui://x", "UI://x", " ui://x", "file:///x", "https://x/ui://y", "uix://"}
	mimes := []string{
		"", "text/html", "text/html;profile=mcp-app", "text/html; profile=mcp-app", "text/html ; profile = mcp-app", "text/html;profile=\"mcp-app\"", "text/html;PROFILE=MCP-APP", "text/html;Profile=Mcp-App", "text/html;profile=mcp-apps", "text/html;profile=mcp-app;x=1",
		"text/html;\u00a0profile=mcp-app", "text/html;profile\u00a0=\u00a0mcp-app", "text/html;\u2003profile=mcp-app", "text/html;\u3000profile=mcp-app", "text/html;\ufeffprofile=mcp-app", "text/html;\u2028profile=mcp-app", "text/html;\u200bprofile=mcp-app", "text/html;\u0085profile=mcp-app", "text/html;\vprofile=mcp-app",
		"text/html;\tprofile=mcp-app", "text/html;\nprofile=mcp-app", "text/html,profile=mcp-app", "text/html profile=mcp-app", "text/html;profile=mcp-ap", "text/html;profile=\"mcp-app", "text/html;profile='mcp-app'", "text/html;profile=mcp-app\"", ";profile=mcp-app", "a;profile=mcp-app;profile=x", "text/html;pro\u212aile=mcp-app", "text/html;profile=mcp-\u017fpp", "text/html;profİle=mcp-app",
	}
	type probe struct {
		URI  string `json:"uri"`
		Mime string `json:"mime"`
	}
	var probes []probe
	for _, uri := range uris {
		for _, mime := range mimes {
			probes = append(probes, probe{uri, mime})
		}
	}
	var want []bool
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/extensions/mcp/resources.js");
const resources = input.map((p, i) => ({ name: String(i), uri: p.uri, mimeType: p.mime }));
const server = { name: "s", timeoutMs: 1000, resourcesPage: async () => ({ resources }) };
const [list] = mod.createMcpResourceToolDefinitions({ exposure: "direct", servers: () => [server] });
const result = await list.execute("probe", { server: "s" }, undefined);
const visible = new Set(result.structuredContent.resources.map((item) => Number(item.name)));
emit(input.map((_, i) => !visible.has(i)));`, probes, &want)
	for i, p := range probes {
		if got := IsMcpAppResource(p.URI, p.Mime); got != want[i] {
			t.Errorf("uri=%q mime=%q: Go %v, Pi %v", p.URI, p.Mime, got, want[i])
		}
	}
}
