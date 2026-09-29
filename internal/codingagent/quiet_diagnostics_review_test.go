package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi resource-loader.ts:1005 uses ??, not ||: a loaded empty name and the literal "unnamed" are distinct.
func TestLoadThemeResourcesEmptyNameIsNotUnnamed(t *testing.T) {
	dir := t.TempDir()
	first := writeNamedTheme(t, dir, "first.json", "")
	named := writeNamedTheme(t, dir, "named.json", "unnamed")
	last := writeNamedTheme(t, dir, "last.json", "")
	registry := tui.NewThemeRegistry()
	_, diagnostics := loadThemeResources(registry, []string{first, named, last})
	want := collisionDiagnostic("theme", "", first, last)
	if len(diagnostics) != 1 || diagnostics[0].Message != want.Message || diagnostics[0].Collision == nil || *diagnostics[0].Collision != *want.Collision {
		t.Fatalf("diagnostics = %+v, want %+v", diagnostics, want)
	}
	if registry.PathOf("unnamed") != named {
		t.Fatalf("literal unnamed theme was lost: %q", registry.PathOf("unnamed"))
	}
}

// Pi replaces registered themes from the resolved resource set on reload (interactive-mode.ts:6230).
// An excluded custom-directory file stays selectable (theme.ts:426-478) but is not a loaded resource; a deleted theme is not retained.
func TestReloadThemeDiagnosticsReplaceResolvedSet(t *testing.T) {
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
	m := reloadTestMode(InteractiveOptions{AgentDir: dir, NoSkills: true, NoPromptTemplates: true, ThemePaths: []string{selected}})
	m.opts.Settings.QuietStartup = true
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

// Pi retains unnamed loaded themes for diagnostics even though setRegisteredThemes skips their empty registry keys.
func TestLoadedEmptyThemeRetainsDiagnosticProvenance(t *testing.T) {
	isolateDisplayHome(t)
	old := tui.ActiveThemeRegistry()
	t.Cleanup(func() { tui.SetThemeRegistry(old) })
	dir := t.TempDir()
	winner := writeNamedTheme(t, dir, "first.json", "")
	loser := writeNamedTheme(t, dir, "second.json", "")
	m := upstreamListingMode(t, false, nil)
	m.opts.Settings.QuietStartup = true
	m.opts.ThemePaths = []string{winner, loser}
	m.resourceSourceInfo = map[string]ResourceSourceInfo{
		winner: {Path: winner, ResourceType: "themes", Enabled: true, Scope: "user", Origin: "package", Source: "npm:empty-theme", BaseDir: dir},
	}
	m.loadThemes()
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
	m.opts.Settings.QuietStartup = true
	m.resourceSourceInfo = map[string]ResourceSourceInfo{
		winner: {Path: winner, ResourceType: "themes", Enabled: true, Scope: "user", Origin: "package", Source: "npm:pkg-a", BaseDir: filepath.Dir(winner)},
	}
	m.themeDiagnostics = []extension.ResourceDiagnostic{collisionDiagnostic("theme", "dusk", winner, loser)}
	m.showLoadedResources(false, true)
	want := "[Theme conflicts]\n  \"dusk\" collision:\n    ✓ npm:pkg-a (user) dusk.json\n    ✗ /pkg-b/dusk.json (skipped)"
	if got := strings.ReplaceAll(renderListing(m), `\`, "/"); got != want {
		t.Fatalf("diagnostics =\n%s\nwant\n%s", got, want)
	}
	m.opts.Settings.QuietStartup = false
	m.showLoadedResources(false, false)
	if got := renderListing(m); !strings.Contains(got, "[Themes]\n  dusk") {
		t.Fatalf("explicit theme missing from non-quiet listing: %s", got)
	}
}
