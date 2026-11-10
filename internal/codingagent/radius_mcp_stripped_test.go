//go:build pig_strip_mcp

package codingagent

// Covers packages/coding-agent/src/core/radius-mcp.ts under pig_strip_mcp (D92: a build without built-in MCP plans and writes nothing); run by make piglet-strip-build

import (
	"os"
	"path/filepath"
	"testing"
)

// strippedRadiusMcpJSON is a global mcp.json that makes the full build plan an `auth` entry for a Radius login: it has the Radius gateway's MCP server
// without it (radius_login_mcp_offer_upstream_test.go runs this case in the full build).
const strippedRadiusMcpJSON = `{"mcpServers":{"radius":{"url":"https://radius.pi.dev/mcp"}}}`

func TestStrippedMcpBuildPlansNoRadiusMcpServer(t *testing.T) {
	agentDir := t.TempDir()
	mcpPath := filepath.Join(agentDir, "mcp.json")
	for name, existing := range map[string]string{"no mcp.json": "", "a Radius server without auth": strippedRadiusMcpJSON} {
		t.Run(name, func(t *testing.T) {
			if existing != "" {
				if err := os.WriteFile(mcpPath, []byte(existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			defer func() { _ = os.Remove(mcpPath) }()
			plan, err := PlanRadiusMcpServer("radius", agentDir, t.TempDir())
			if plan != nil || err != nil {
				t.Fatalf("plan %+v, err %v; want none", plan, err)
			}
			data, readErr := os.ReadFile(mcpPath)
			if existing == "" && !os.IsNotExist(readErr) {
				t.Fatalf("planning created %s: %q, %v", mcpPath, data, readErr)
			}
			if existing != "" && string(data) != existing {
				t.Fatalf("planning changed %s: %q", mcpPath, data)
			}
		})
	}
}

func TestStrippedMcpBuildApplyWritesNothing(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp.json")
	plan := &RadiusMcpServerPlan{McpPath: mcpPath, Name: "radius", Config: []byte(`{"url":"https://radius.pi.dev/mcp","auth":{"provider":"radius"}}`)}
	replaced, err := plan.Apply()
	if replaced || err != nil {
		t.Fatalf("Apply = %v, %v; want false, nil", replaced, err)
	}
	if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
		t.Fatalf("Apply wrote %s: %v", mcpPath, err)
	}

	var none *RadiusMcpServerPlan
	if replaced, err := none.Apply(); replaced || err != nil {
		t.Fatalf("Apply on no plan = %v, %v; want false, nil", replaced, err)
	}
}
