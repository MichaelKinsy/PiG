package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// 42a3497d theme-schema.ts: ThemeJsonSchema.name is String({pattern: "^[^/]+$"}), so a user theme with an empty name is rejected with
// a schema warning (it can no longer collide as ""), while the literal name "unnamed" is an ordinary name (resource-loader.ts uses ??, not ||).
func TestLoadThemeResourcesRejectsAnEmptyNameAndKeepsTheLiteralUnnamed(t *testing.T) {
	dir := t.TempDir()
	first := writeNamedTheme(t, dir, "first.json", "")
	named := writeNamedTheme(t, dir, "named.json", "unnamed")
	last := writeNamedTheme(t, dir, "last.json", "")
	registry := tui.NewThemeRegistry()
	_, diagnostics := loadThemeResources(registry, []string{first, named, last}, tui.GetTerminalColorMode())
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %+v, want one warning per empty-name theme", diagnostics)
	}
	for i, path := range []string{first, last} {
		want := "Invalid theme \"" + path + "\":\n\n\nOther errors:\n  - /name: must match pattern \"^[^/]+$\""
		if diagnostics[i].Type != "warning" || diagnostics[i].Path != path || diagnostics[i].Message != want {
			t.Fatalf("diagnostic %d = %+v, want warning %q", i, diagnostics[i], want)
		}
	}
	if registry.PathOf("unnamed") != named {
		t.Fatalf("literal unnamed theme was lost: %q", registry.PathOf("unnamed"))
	}
}

// Pi replaces registered themes from the resolved resource set on reload (interactive-mode.ts:6230).
// An excluded custom-directory file stays selectable (theme.ts:426-478) but is not a loaded resource; a deleted theme is not retained.
func TestReloadThemeDiagnosticsReplaceResolvedSet(t *testing.T) {
	restoreStartupTheme(t) // Reload applies the theme setting, which selects the process-wide active theme.
	old := tui.ActiveThemeRegistry()
	t.Cleanup(func() { tui.SetThemeRegistry(old) })
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	dir := t.TempDir()
	themesDir := filepath.Join(dir, "themes")
	if err := os.MkdirAll(themesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeNamedTheme(t, themesDir, "excluded.json", "excluded")
	selected := writeNamedTheme(t, themesDir, "selected.json", "selected")
	m := reloadTestMode(InteractiveModeOptions{AgentDir: dir, NoSkills: true, NoPromptTemplates: true, ThemePaths: []string{selected}})
	m.opts.Settings.QuietStartup = QuietStartupTrue
	slash := m.buildSlashContext(t.Context())
	if err := slash.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(m.loadedThemes) != 1 || m.loadedThemes[0].path != selected {
		t.Errorf("reload bypassed resolved-path filtering: %#v", m.loadedThemes)
	}
	if path := tui.ActiveThemeRegistry().PathOf("excluded"); path != filepath.Join(themesDir, "excluded.json") {
		t.Errorf("excluded custom theme is not selectable: %q", path)
	}
	if err := os.Remove(selected); err != nil {
		t.Fatal(err)
	}
	if err := slash.Reload(); err != nil {
		t.Fatal(err)
	}
	if path := tui.ActiveThemeRegistry().PathOf("selected"); path != "" {
		t.Errorf("reload retained removed theme: %q", path)
	}
	if got := renderListing(m); strings.Count(got, "theme path does not exist") != 1 {
		t.Fatalf("reload diagnostics = %q", got)
	}
	m.opts.ThemePaths = nil
	if err := slash.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := renderListing(m); got != "" {
		t.Fatalf("resolved diagnostic not cleared: %q", got)
	}
}

// Pi retains unnamed loaded themes for diagnostics even though setRegisteredThemes skips their empty registry keys. A user theme file can no
// longer carry an empty name (42a3497d theme-schema.ts), so the loaded set and the collision are supplied directly.
func TestLoadedEmptyThemeRetainsDiagnosticProvenance(t *testing.T) {
	isolateDisplayHome(t)
	old := tui.ActiveThemeRegistry()
	t.Cleanup(func() { tui.SetThemeRegistry(old) })
	dir := t.TempDir()
	winner, loser := filepath.Join(dir, "first.json"), filepath.Join(dir, "second.json")
	m := upstreamListingMode(t, false, nil)
	m.opts.Settings.QuietStartup = QuietStartupTrue
	m.loadedThemes = []loadedTheme{{theme: &tui.Theme{Name: ""}, path: winner}}
	m.resourceSourceInfo = map[string]ResourceSourceInfo{
		winner: {Path: winner, ResourceType: "themes", Enabled: true, Scope: "user", Origin: "package", Source: "npm:empty-theme", BaseDir: dir},
	}
	m.themeDiagnostics = []extension.ResourceDiagnostic{collisionDiagnostic("theme", "", winner, loser)}
	m.showLoadedResources(false, true)
	want := "[Theme conflicts]\n  \"\" collision:\n    ✓ npm:empty-theme (user) first.json\n    ✗ " + filepath.ToSlash(loser) + " (skipped)"
	if got := strings.ReplaceAll(renderListing(m), `\`, "/"); got != want {
		t.Fatalf("diagnostics =\n%s\nwant\n%s", got, want)
	}
}

// Pi interactive-mode.ts:1760-1765 gathers metadata from loaded themes even when --no-themes disables discovery.
func TestLoadedExplicitThemeDiagnosticsRetainSourceWithNoThemes(t *testing.T) {
	old := tui.ActiveThemeRegistry()
	t.Cleanup(func() { tui.SetThemeRegistry(old) })
	winner, loser := filepath.FromSlash("/pkg-a/dusk.json"), filepath.FromSlash("/pkg-b/dusk.json")
	m := upstreamListingMode(t, false, nil)
	m.loadedThemes = []loadedTheme{{theme: &tui.Theme{Name: "dusk"}, path: winner}}
	m.opts.NoThemes = true
	m.opts.Settings.QuietStartup = QuietStartupTrue
	m.resourceSourceInfo = map[string]ResourceSourceInfo{
		winner: {Path: winner, ResourceType: "themes", Enabled: true, Scope: "user", Origin: "package", Source: "npm:pkg-a", BaseDir: filepath.Dir(winner)},
	}
	m.themeDiagnostics = []extension.ResourceDiagnostic{collisionDiagnostic("theme", "dusk", winner, loser)}
	m.showLoadedResources(false, true)
	want := "[Theme conflicts]\n  \"dusk\" collision:\n    ✓ npm:pkg-a (user) dusk.json\n    ✗ /pkg-b/dusk.json (skipped)"
	if got := strings.ReplaceAll(renderListing(m), `\`, "/"); got != want {
		t.Fatalf("diagnostics =\n%s\nwant\n%s", got, want)
	}
	m.opts.Settings.QuietStartup = QuietStartupFalse
	m.showLoadedResources(false, false)
	// upstream 0.99.1 interactive-mode.ts showLoadedResources has no Themes section.
	if got := renderListing(m); strings.Contains(got, "[Themes]") {
		t.Fatalf("non-quiet listing shows a theme section: %s", got)
	}
}
