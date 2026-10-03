package mcpext_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// Implementation-derived (Pi 1.0.0 adds no test for the shorter section): the first line of the `mcp_servers` section
// explains only the ways of reaching tools that the listed servers use
// (.upstream/v1.0.0/packages/coding-agent/src/extensions/mcp/index.ts:158-164 serversSectionIntro, 193-197).
func TestMCPServersSectionIntroExplainsOnlyTheReachesOfTheListedServers(t *testing.T) {
	const declared = "MCP servers whose tools are not declared to you."
	const codemode = " Call the tools of `codemode` servers from codemode scripts."
	const toolSearch = " Load the tools of `tool_search` servers with `tool_search`."
	for _, tc := range []struct {
		name    string
		servers []mcpext.McpServerListing
		intro   string
	}{
		{"codemode servers", []mcpext.McpServerListing{sectionServer("docs", "Docs.", "")}, declared + codemode},
		{"tool_search servers", []mcpext.McpServerListing{sectionServer("later", "", extension.McpExposureDeferred)}, declared + toolSearch},
		{"both", []mcpext.McpServerListing{sectionServer("docs", "Docs.", ""), sectionServer("later", "", extension.McpExposureDeferred)}, declared + codemode + toolSearch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			section, ok := mcpext.RenderServersSection(tc.servers)
			if !ok {
				t.Fatal("no section")
			}
			if first, _, _ := strings.Cut(section, "\n"); first != tc.intro {
				t.Fatalf("intro = %q, want %q", first, tc.intro)
			}
		})
	}
}
