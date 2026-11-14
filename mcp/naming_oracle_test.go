package mcp_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Pi's mcpServerSummary (packages/mcp/src/naming.ts, Pi 3567987250) as the vendored pi-mcp runs it: the oracle carries the commit
// through automation/gen/pi-post-release-backports.mjs, so a Pi without it answers with a missing export.
func TestMcpServerSummaryMatchesPiOracle(t *testing.T) {
	// Each pair is {description, instructions}; null is Pi's undefined, which the Go port spells "".
	inputs := [][2]*string{
		{new(" Docs search.\nMore. "), new("Ignored.")},
		{nil, new("\n  Web search for current information.\nDetails.")},
		{new("  "), new("From instructions.")},
		{nil, nil},
		{new(""), new("")},
		{new("\n"), new(" \n\t ")},
		{nil, new("   \n  second\nthird")},
		{new("\u00a0First\u00a0line\u00a0\nrest"), nil},
		{new("\ufeffBom\nnext"), nil},
		{nil, new("\r\nCRLF first\r\nsecond")},
		{new("one line"), nil},
		{new("é\u2028not a newline for split"), nil},
	}
	var want []string
	pioracle.Run(t, `
const mod = await load("pi-mcp/naming.js");
emit(input.map(([description, instructions]) => mod.mcpServerSummary(description ?? undefined, instructions ?? undefined)));`, inputs, &want)
	for i, in := range inputs {
		var description, instructions string
		if in[0] != nil {
			description = *in[0]
		}
		if in[1] != nil {
			instructions = *in[1]
		}
		if got := mcp.McpServerSummary(description, instructions); got != want[i] {
			t.Errorf("McpServerSummary(%q, %q) = %q, Pi %q", description, instructions, got, want[i])
		}
	}
}
