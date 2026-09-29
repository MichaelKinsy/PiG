package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/package-manager.test.ts:643: auto-discovery returns extensions/main.ts and themes/dark.json, not the enclosing directories.
func TestPackageAutoDiscoveryLayoutUpstream(t *testing.T) {
	cwd, agent := t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PIG_HOME", t.TempDir())
	pkg := filepath.Join(cwd, "auto-pkg")
	extension, theme := filepath.Join(pkg, "extensions", "main.ts"), filepath.Join(pkg, "themes", "dark.json")
	for _, file := range []struct{ path, contents string }{{extension, "export default function() {}"}, {theme, "{}"}} {
		if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.path, []byte(file.contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agent)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
		t.Fatal(err)
	}
	items := resourceSourceInfoProvider(cwd, agent, sm, CLIFlags{})()
	for _, want := range []struct {
		path string
		kind tui.ResourceType
	}{{extension, tui.ResourceExtensions}, {theme, tui.ResourceThemes}} {
		found := false
		for _, item := range items {
			if item.Path == want.path && item.ResourceType == string(want.kind) && item.Enabled {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing enabled %s at %s: %+v", want.kind, want.path, items)
		}
	}
	configs := collectExtensionConfigs(cwd, agent, sm, CLIFlags{}, nil)
	if len(configs) != 1 || configs[0].Source != extension || !configs[0].Enabled {
		t.Fatalf("runtime extension configs = %+v, want %s", configs, extension)
	}
}
