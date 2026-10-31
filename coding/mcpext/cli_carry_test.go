package mcpext_test

// pi: packages/coding-agent/src/extensions/mcp/cli.ts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// Ports the three tests Pi be15fe3c added to mcp-command.test.ts (Pi carries no issue number for it).

func TestMcpCommandSuggestsAConfiguredServerForAMistypedName(t *testing.T) {
	// mcp-command.test.ts "suggests a configured server for a mistyped name"
	result := runMcp(t, []string{"login", "fixtures"}, commandServers(), "")
	want := `No MCP server named "fixtures". Did you mean "fixture"? Configured: fixture, broken, parked.`
	if result.output != want {
		t.Errorf("output = %q, want %q", result.output, want)
	}
}

func TestMcpCommandReportsOptionsAsTyped(t *testing.T) {
	// mcp-command.test.ts "reports options as typed"
	if result := runMcp(t, []string{"list", "-l"}, nil, ""); !strings.Contains(result.output, "Unknown option -l.") {
		t.Errorf("output = %q", result.output)
	}
}

func TestMcpCommandRestrictsLoginAndLogoutToProjectServersWithLocal(t *testing.T) {
	// mcp-command.test.ts "restricts login and logout to project servers with --local"
	agentDir, project := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(`{"mcpServers":{"fixture":`+fixtureServer()+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string) {
		var output []string
		add := func(line string) { output = append(output, line) }
		code := mcpext.RunMcpCommand(context.Background(), args, mcpext.McpCommandOptions{
			Cwd: project, AgentDir: agentDir, AppName: "pig", ConfigDirName: ".pig", Log: add, Error: add,
			IsProjectTrusted: func(string) (bool, error) { return true, nil },
		})
		return code, strings.Join(output, "\n")
	}
	if code, out := run("add", "-l", "local", "--", "node", "server.js"); code != 0 {
		t.Fatalf("add = %d %q", code, out)
	}
	if code, out := run("logout", "-l", "local"); code != 1 || out != `MCP server "local" does not use OAuth. Only HTTP servers without an Authorization header do.` {
		t.Errorf("logout -l local = %d %q", code, out)
	}
	want := `MCP server "fixture" is defined in ` + filepath.Join(agentDir, "mcp.json") + `, not in the project; omit --local.`
	if code, out := run("login", "-l", "fixture"); code != 1 || out != want {
		t.Errorf("login -l fixture = %d %q, want %q", code, out, want)
	}
}
