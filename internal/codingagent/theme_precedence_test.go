package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/resource-loader.test.ts:123-181 at the theme-loading boundary. The CLI collector's matching project/user order is checked by TestResourceLoaderUpstreamProjectPrecedence in cmd/pig.
func TestResourceLoaderUpstreamThemePrecedence(t *testing.T) {
	data, err := os.ReadFile("../../tui/theme_dark.json")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	userPath := filepath.Join(root, "agent", "themes", "collision.json")
	projectPath := filepath.Join(root, "project", ".pig", "themes", "collision.json")
	for _, path := range []string{userPath, projectPath} {
		content := strings.Replace(string(data), `"name": "dark"`, `"name": "collision-theme"`, 1)
		if path == projectPath {
			content = strings.Replace(content, `"accent": "#8abeb7"`, `"accent": "#ff00ff"`, 1)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry := tui.NewThemeRegistry()
	if _, diagnostics := loadThemeResources(registry, []string{projectPath, userPath}, tui.GetTerminalColorMode()); len(diagnostics) != 1 || diagnostics[0].Collision == nil {
		t.Fatalf("diagnostics = %#v, want the one collision", diagnostics)
	}
	if got := registry.PathOf("collision-theme"); got != projectPath {
		t.Fatalf("theme source = %s, want %s", got, projectPath)
	}
}

// Upstream dedupeThemes keeps the first theme of a name in precedence order
// (resource-loader.ts); loadThemeResources gives the registry the same winner.
func TestLoadThemePathsKeepsTheFirstThemeOfAName(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "test/parity", "scenarios", "startup", "testdata", "listing", "pig-agent", "themes", "fixture-theme.json"))
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
	// The assertion compares a 24-bit color, so pin the capability instead of inheriting the host's COLORTERM.
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	registry := tui.NewThemeRegistry()
	loadThemeResources(registry, []string{first, second}, tui.GetTerminalColorMode())
	theme := registry.Get("fixture-theme")
	if theme == nil || !strings.Contains(theme.Accent, "138;190;183") {
		t.Fatalf("fixture-theme accent = %q, want the first path's #8abeb7", theme.Accent)
	}
	// The startup listing names a theme only when its source file is known,
	// so a theme loaded from a file path (every Package theme) records it.
	if got := registry.PathOf("fixture-theme"); got != first {
		t.Fatalf("PathOf(fixture-theme) = %q, want %q", got, first)
	}
}
