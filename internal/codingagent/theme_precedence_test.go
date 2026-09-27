package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream dedupeThemes keeps the first theme of a name in precedence order
// (resource-loader.ts); loadThemePaths gives the registry the same winner.
func TestLoadThemePathsKeepsTheFirstThemeOfAName(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "parity", "scenarios", "startup", "testdata", "listing", "pig-agent", "themes", "fixture-theme.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	first := filepath.Join(dir, "project", "fixture-theme.json")
	second := filepath.Join(dir, "user", "fixture-theme.json")
	for i, path := range []string{first, second} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		content := string(fixture)
		if i == 1 {
			content = strings.Replace(content, `"accent": "#8abeb7"`, `"accent": "#ff0000"`, 1)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry := tui.NewThemeRegistry()
	loadThemePaths(registry, []string{first, second}, func(err error) { t.Error(err) })
	theme := registry.Get("fixture-theme")
	if theme == nil || !strings.Contains(theme.Accent, "138;190;183") {
		t.Fatalf("fixture-theme accent = %q, want the first path's #8abeb7", theme.Accent)
	}
}
