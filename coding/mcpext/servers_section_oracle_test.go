package mcpext_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// The mcp_servers section of the vendored coding agent (extensions/mcp/index.js renderServersSection) against RenderServersSection, with
// server instructions that start with a blank line: Pi 3567987250 reads their first non-blank line, which the oracle carries through
// automation/gen/pi-post-release-backports.mjs (the pin reads an empty summary).
func TestMCPServersSectionMatchesPiOracle(t *testing.T) {
	type server struct {
		Name         string `json:"name"`
		Description  string `json:"description,omitempty"`
		Instructions string `json:"instructions,omitempty"`
	}
	servers := []server{
		{Name: "blank-first", Instructions: "\n  Web search for current information.\nDetails."},
		{Name: "described", Description: " Docs search.\nMore. ", Instructions: "Ignored."},
		{Name: "spaces", Description: "  ", Instructions: "   \n  second\nthird"},
		{Name: "whitespace-only", Instructions: " \n\t "},
		{Name: "none"},
	}
	var want string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/extensions/mcp/index.js");
emit(mod.renderServersSection(input.map((s) => ({ entry: { name: s.name, config: { command: "x", description: s.description || undefined } }, connection: s.instructions ? { instructions: s.instructions } : undefined }))) ?? "");`, servers, &want)
	var listing []mcpext.McpServerListing
	for _, s := range servers {
		listing = append(listing, mcpext.McpServerListing{
			Entry:        mcpext.McpServerEntry{Name: s.Name, Source: "test", Config: extension.McpServerConfig{Command: "x", Description: s.Description}},
			Instructions: s.Instructions,
		})
	}
	got, _ := mcpext.RenderServersSection(listing)
	if got != want {
		t.Fatalf("section differs from Pi\n got: %q\nwant: %q", got, want)
	}
}
