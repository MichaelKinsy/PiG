//go:build !pig_strip_mcp

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A sign-in to Radius adds the Radius MCP server to mcp.json; a build without the MCP extension offers no such server.
func TestAuthLoginKeepsUnrelatedMCPServersWhenTheFallbackNameIsOccupied(t *testing.T) {
	for _, fallback := range []string{"radius-mcp", "radius_mcp"} {
		t.Run(fallback, func(t *testing.T) {
			agentDir := t.TempDir()
			runtime, _ := authTestRuntime(t, nil, authTestProvider(apiKeyOnly(), ai.RadiusProviderID))
			mcpPath := filepath.Join(agentDir, "mcp.json")
			servers := map[string]any{
				"radius":       map[string]any{"command": "original-one"},
				fallback:       map[string]any{"command": "original-two"},
				"radius-mcp-2": map[string]any{"command": "original-three"},
			}
			data, _ := json.Marshal(map[string]any{"mcpServers": servers})
			if err := os.WriteFile(mcpPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			terminal := newScriptedTerminal("sk-test")
			if code := runAuthLoginForTest(t.Context(), []string{"radius"}, runtime, terminal, agentDir); code != 0 {
				t.Fatalf("code %d, errors %q", code, terminal.errors)
			}
			var written struct {
				McpServers map[string]any `json:"mcpServers"`
			}
			raw, err := os.ReadFile(mcpPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &written); err != nil {
				t.Fatal(err)
			}
			servers["radius-mcp-3"] = map[string]any{"url": "https://radius.pi.dev/mcp", "auth": map[string]any{"provider": "radius"}}
			if !reflect.DeepEqual(written.McpServers, servers) {
				t.Fatalf("servers %v, want %v", written.McpServers, servers)
			}
			if want := `Added MCP server "radius-mcp-3" in ` + mcpPath + " to use the Login Test login."; !terminal.contains(want) {
				t.Fatalf("lines %q", terminal.lines)
			}
		})
	}
}

func TestAuthLoginConfiguresTheRadiusMCPServerWithoutAskingOnce(t *testing.T) {
	agentDir := t.TempDir()
	runtime, _ := authTestRuntime(t, nil, authTestProvider(apiKeyOnly(), ai.RadiusProviderID))
	mcpPath := filepath.Join(agentDir, "mcp.json")
	first := newScriptedTerminal("sk-test")
	if code := runAuthLoginForTest(t.Context(), []string{"radius"}, runtime, first, agentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, first.errors)
	}
	if !reflect.DeepEqual(first.questions, []string{"Enter Login Test API key: "}) || !first.contains(`Added MCP server "radius" in `+mcpPath+" to use the Login Test login.") {
		t.Fatalf("questions %q, lines %q", first.questions, first.lines)
	}
	raw, err := os.ReadFile(mcpPath)
	if err != nil || !strings.Contains(string(raw), `"auth":{"provider":"radius"}`) && !strings.Contains(string(raw), `"provider": "radius"`) {
		t.Fatalf("mcp.json %q, %v", raw, err)
	}
	second := newScriptedTerminal("sk-test")
	if code := runAuthLoginForTest(t.Context(), []string{"radius"}, runtime, second, agentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, second.errors)
	}
	if slices.ContainsFunc(second.lines, func(line string) bool { return strings.Contains(line, "MCP server") }) {
		t.Fatalf("lines %q", second.lines)
	}
}
