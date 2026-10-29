package mcpext

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const closedConfigServers = `{"mcpServers":{"srv":{"command":"/bin/true"}}}`

func TestRefuseConfigFilesDropsServersFromMcpJSON(t *testing.T) {
	agentDir, project := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(closedConfigServers), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".pig", "mcp.json"), []byte(`{"mcpServers":{"proj":{"command":"/bin/true"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := EventContext{Context: context.Background(), Cwd: project, IsProjectTrusted: func() bool { return true }}

	open := &Extension{options: Options{AgentDir: agentDir, ConfigDirName: ".pig"}}
	if got := open.loadConfig(ctx); len(got.Servers) != 2 || len(got.Errors) != 0 {
		t.Fatalf("default load = %d servers, errors %v; want 2 servers", len(got.Servers), got.Errors)
	}

	const message = "pig: this Piglet Binary (x) was built with build.closed: true, so it does not accept mcp.json"
	closed := &Extension{options: Options{AgentDir: agentDir, ConfigDirName: ".pig", RefuseConfigFiles: message}}
	got := closed.loadConfig(ctx)
	if len(got.Servers) != 0 || got.ProjectConfig != "" {
		t.Fatalf("closed load kept servers %v / project config %q", got.Servers, got.ProjectConfig)
	}
	if len(got.Errors) != 1 || got.Errors[0] != message {
		t.Fatalf("closed load errors = %v, want the refusal", got.Errors)
	}

	empty := &Extension{options: Options{AgentDir: t.TempDir(), ConfigDirName: ".pig", RefuseConfigFiles: message}}
	emptyCtx := EventContext{Context: context.Background(), Cwd: t.TempDir(), IsProjectTrusted: func() bool { return true }}
	if got := empty.loadConfig(emptyCtx); len(got.Errors) != 0 {
		t.Fatalf("closed load with no mcp.json reported %v", got.Errors)
	}
}
