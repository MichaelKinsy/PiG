//go:build !pig_strip_node_extensions

package codingagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Upstream lists tool and flag conflicts under [Extension issues] after
// /reload; both extensions stay loaded.
func TestReloadListsExtensionToolConflicts(t *testing.T) {
	restoreStartupTheme(t) // Reload applies the theme setting, which selects the process-wide active theme.
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	// See the comment in TestReloadListsAFailingExtensionOnce: pin PIG_HOME so
	// subprocess.NewHost never falls back to the operator's real ~/.pig/cache.
	t.Setenv("PIG_HOME", filepath.Join(t.TempDir(), "pig-home"))
	dir := t.TempDir()
	source := "export default function (pi) { pi.registerTool({ name: \"ask_user\", label: \"Ask\", description: \"ask\", parameters: { type: \"object\", properties: {} }, execute: async () => ({ content: [{ type: \"text\", text: \"ok\" }] }) }); }\n"
	var paths []string
	for _, name := range []string{"one", "two"} {
		path := filepath.Join(dir, name, "ask.mjs")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) {
		return []subprocess.ExtConfig{{Name: "ask", Source: paths[0], Enabled: true}, {Name: "ask", Source: paths[1], Enabled: true}}, nil
	})
	m := reloadTestMode(InteractiveModeOptions{SubprocessHost: host})
	slash := m.buildSlashContext(context.Background())
	if err := slash.Reload(); err != nil {
		t.Fatal(err)
	}
	want := extension.ResourceDiagnostic{Type: extension.DiagnosticError, Path: paths[1], Message: `Tool "ask_user" conflicts with ` + paths[0]}
	if diagnostics := m.extensionDiagnostics(); !slices.Contains(diagnostics, want) {
		t.Fatalf("diagnostics = %#v, want %#v", diagnostics, want)
	}
	if host.ExtensionCount() != 2 {
		t.Fatalf("loaded extensions = %d, want both copies", host.ExtensionCount())
	}
}
