package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/coding-agent/test/resource-loader.test.ts:183-211. resource-loader.ts:850-864 retains the first selected alias after canonical-path deduplication, before executing any factories.
func TestUpstreamResourceLoaderSymlinkedExtensions(t *testing.T) {
	root := t.TempDir()
	agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "project")
	t.Setenv("PIG_HOME", filepath.Join(root, "config"))
	shared := filepath.Join(root, "shared-extensions")
	for _, dir := range []string{agentDir, filepath.Join(cwd, ".pig"), shared} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(shared, "shared.ts"), []byte(`export default function(pi) {
  pi.registerCommand("shared", { description: "shared command", handler: async () => {} });
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Upstream's "dir" symlinks (resource-loader.test.ts:198-199) need privilege on Windows; a junction there is the same symbolic link to Node.
	testenv.RequireDirectoryLink(t, shared, filepath.Join(agentDir, "extensions"))
	testenv.RequireDirectoryLink(t, shared, filepath.Join(cwd, ".pig", "extensions"))
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	configs := collectExtensionConfigs(cwd, agentDir, sm, Args{}, nil)
	host := subprocess.NewHostWithConfigRoot(cwd, filepath.Join(root, "host"))
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, errs := host.LoadAll(t.Context(), configs)
	if len(errs) != 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d extensions, want one shared factory: %#v", len(loaded), loaded)
	}
	want := filepath.Join(cwd, ".pig", "extensions", "shared.ts")
	if loaded[0].Path != want {
		t.Fatalf("selected path = %q, want project alias %q", loaded[0].Path, want)
	}
	if len(loaded[0].Commands) != 1 || loaded[0].Commands["shared"].Name != "shared" {
		t.Fatalf("commands = %#v", loaded[0].Commands)
	}
}
