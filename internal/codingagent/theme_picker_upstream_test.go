package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// theme-picker.test.ts:34; startup's directory and explicit-file loading paths must expose the content name and its source path through getAllThemes.
func TestThemeContentNamePathReachesExtension(t *testing.T) {
	data, err := os.ReadFile("../../tui/theme_dark.json")
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]json.RawMessage
	if err := json.Unmarshal(data, &theme); err != nil {
		t.Fatal(err)
	}
	theme["name"] = json.RawMessage(`"bar"`)
	data, err = json.MarshalIndent(theme, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "agent", "themes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "foo.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{dir, path} {
		t.Run(filepath.Base(source), func(t *testing.T) {
			registry := tui.NewThemeRegistry()
			if _, diagnostics := loadThemeResources(registry, []string{source}, tui.GetTerminalColorMode()); len(diagnostics) != 0 {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
			old := tui.ActiveThemeRegistry()
			t.Cleanup(func() { tui.SetThemeRegistry(old) })
			tui.SetThemeRegistry(registry)
			ui := &ExtUIContext{}
			found := false
			for _, meta := range ui.GetAllThemes() {
				if meta.Name == "foo" {
					t.Fatal("filename exposed as theme name")
				}
				if meta.Name == "bar" {
					found = true
					if meta.Path != path {
						t.Fatalf("bar path = %q, want %q", meta.Path, path)
					}
				}
			}
			if !found {
				t.Fatal("missing bar theme")
			}
		})
	}
}
