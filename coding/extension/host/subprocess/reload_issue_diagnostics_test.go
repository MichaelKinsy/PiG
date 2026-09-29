package subprocess

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestReloadNodeFactoryDiagnosticMessages(t *testing.T) {
	nodeCellRequireNode(t)
	for _, invalidExport := range []bool{false, true} {
		t.Run(map[bool]string{false: "throw", true: "invalid-export"}[invalidExport], func(t *testing.T) {
			root := t.TempDir()
			source := `export default function () { throw new Error("reload boom"); }`
			if invalidExport {
				source = `export default {};`
			}
			cfg := startupNodeFixture(t, root, "broken", source)
			host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
			t.Cleanup(func() { host.Shutdown("test done") })
			host.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{cfg}, nil })
			if loaded, err := host.Reload(t.Context()); err != nil || len(loaded) != 0 {
				t.Fatalf("reload = %v, %v", loaded, err)
			}
			message := "Failed to load extension: reload boom"
			if invalidExport {
				message = "Extension does not export a valid factory function: " + cfg.Source
			}
			want := cfg.Source + ": " + message
			report := host.LastReloadReport()
			if report == nil || len(report.Issues) != 1 || report.Issues[0] != want {
				t.Fatalf("reload report = %+v, want issue %q", report, want)
			}
		})
	}
}

// Pi extensions/loader.ts:570,578 supplies the complete diagnostic before the host formats its path.
func TestReloadIssuePreservesFactoryLoadDiagnostic(t *testing.T) {
	cfg := ExtConfig{Name: "broken", Source: "/ext/broken.ts"}.SelectedAs("/ext/broken.ts")
	for _, message := range []string{
		"Failed to load extension: boom",
		"Extension does not export a valid factory function: /ext/broken.ts",
	} {
		err := newLoadError(cfg.Name, "load", "load_failed", &FactoryLoadError{Message: message})
		if got, want := reloadIssue(cfg, err), "/ext/broken.ts: "+message; got != want {
			t.Errorf("reload issue = %q, want %q", got, want)
		}
	}
	if got, want := reloadIssue(cfg, errors.New("build failed")), "/ext/broken.ts: Failed to load extension: build failed"; got != want {
		t.Errorf("native build issue = %q, want %q", got, want)
	}
}
