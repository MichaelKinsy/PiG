package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// TestPackageLayeredFiltersUpstream ports package-manager.test.ts:1707-1743.
// Manifest exclusions remove candidates; user exclusions retain disabled entries.
func TestPackageLayeredFiltersUpstream(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", cwd)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	root := filepath.Join(cwd, "layered-pkg")
	if err := os.MkdirAll(filepath.Join(root, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"foo.ts", "bar.ts", "baz.ts"} {
		if err := os.WriteFile(filepath.Join(root, "extensions", name), []byte("export default function() {}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"layered-pkg","pi":{"extensions":["extensions","!**/baz.ts"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{
		Source: root, Extensions: []string{"!**/bar.ts"},
		Skills: []string{}, Prompts: []string{}, Themes: []string{},
	}}); err != nil {
		t.Fatal(err)
	}
	infos := resourceSourceInfoProvider(cwd, agentDir, settings, Args{})()
	for name, enabled := range map[string]bool{"foo.ts": true, "bar.ts": false} {
		path := filepath.Join(root, "extensions", name)
		info, ok := infos[path]
		if !ok || info.Enabled != enabled {
			t.Errorf("%s: present=%t enabled=%t, want present=true enabled=%t", name, ok, info.Enabled, enabled)
		}
	}
	if _, ok := infos[filepath.Join(root, "extensions", "baz.ts")]; ok {
		t.Error("manifest-excluded baz.ts remains a candidate")
	}
	// The same filter must control actual startup admission, not only /config.
	configs := collectExtensionConfigs(cwd, agentDir, settings, Args{}, nil)
	var sources []string
	for _, config := range configs {
		sources = append(sources, config.Source)
	}
	if want := []string{filepath.Join(root, "extensions", "foo.ts")}; !slices.Equal(sources, want) {
		t.Errorf("startup extensions = %v, want %v", sources, want)
	}
}
