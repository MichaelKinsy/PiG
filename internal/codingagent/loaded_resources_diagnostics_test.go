package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

func collisionDiagnostic(kind, name, winner, loser string) extension.ResourceDiagnostic {
	return extension.ResourceDiagnostic{
		Type: extension.DiagnosticCollision, Message: `name "` + name + `" collision`, Path: loser,
		Collision: &extension.ResourceCollision{ResourceType: kind, Name: name, WinnerPath: winner, LoserPath: loser},
	}
}

// diagnosticsFixtureMode carries one diagnostic of every kind
// showLoadedResources reports: skill and prompt collisions, an extension load
// error, built-in command conflicts (single and disambiguated), a shortcut
// conflict, and theme collision and load warnings.
func diagnosticsFixtureMode(t testing.TB) *InteractiveMode {
	t.Helper()
	m := upstreamListingMode(t, false, nil)
	command := func(name, path string) map[string]extension.RegisteredCommand {
		return map[string]extension.RegisteredCommand{name: {Name: name, SourceInfo: PiSourceInfo{Path: path, Source: "local", Scope: "project", Origin: "top-level"}}}
	}
	m.newRunner = inproc.NewRunner([]extension.Extension{
		{Path: "/ext/cmd.ts", Commands: command("model", "/ext/cmd.ts"), CommandOrder: []string{"model"}},
		{Path: "/ext/tree-a.ts", Commands: command("tree", "/ext/tree-a.ts"), CommandOrder: []string{"tree"}},
		{Path: "/ext/tree-b.ts", Commands: command("tree", "/ext/tree-b.ts"), CommandOrder: []string{"tree"}},
		{Path: "/ext/sc.ts", Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{
			"ctrl+c": {Shortcut: "ctrl+c", ExtensionPath: "/ext/sc.ts"},
		}},
	}, "/tmp/project")
	m.newRunner.Shortcuts(map[string][]string{"app.clear": {"ctrl+c"}})
	m.opts.SubprocessHost = &reportingHost{report: &subprocess.ReloadReport{Issues: []string{"/ext/broken.ts: Failed to load extension: boom"}}}
	m.opts.SkillDiagnostics = []extension.ResourceDiagnostic{collisionDiagnostic("skill", "commit", "/s/a/SKILL.md", "/s/b/SKILL.md")}
	m.promptDiagnostics = []extension.ResourceDiagnostic{collisionDiagnostic("prompt", "review", "/p/a.md", "/p/b.md")}
	m.themeDiagnostics = []extension.ResourceDiagnostic{
		collisionDiagnostic("theme", "sunset", "/t/a.json", "/t/b.json"),
		{Type: extension.DiagnosticWarning, Message: "theme parse boom", Path: "/t/bad.json"},
	}
	return m
}

const wantDiagnosticsBlock = `[Skill conflicts]
  "commit" collision:
    ✓ /s/a/SKILL.md
    ✗ /s/b/SKILL.md (skipped)

[Prompt conflicts]
  "review" collision:
    ✓ /p/a.md
    ✗ /p/b.md (skipped)

[Extension issues]
  /ext/broken.ts
    Failed to load extension: boom
  /ext/cmd.ts
    Extension command '/model' conflicts with built-in interactive command. Skipping in autocomplete.
  /ext/tree-a.ts
    Extension command '/tree' conflicts with built-in interactive command. Available as '/tree:1'.
  /ext/tree-b.ts
    Extension command '/tree' conflicts with built-in interactive command. Available as '/tree:2'.
  /ext/sc.ts
    Extension shortcut 'ctrl+c' from /ext/sc.ts conflicts with built-in shortcut. Skipping.

[Theme conflicts]
  "sunset" collision:
    ✓ /t/a.json
    ✗ /t/b.json (skipped)
  /t/bad.json
    theme parse boom`

// isolateDisplayHome sets the home directory to one that no other t.TempDir path starts with. Pi's formatDisplayPath prints a path that starts with os.homedir() (USERPROFILE on Windows, HOME elsewhere) as "~" plus the rest, and Windows places t.TempDir under the user profile, so a diagnostic that asserts a full temp path needs this.
func isolateDisplayHome(t *testing.T) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func renderListing(m *InteractiveMode) string {
	lines := m.loadedResourcesContainer.Render(220)
	for i, line := range lines {
		lines[i] = strings.TrimRight(stripANSITest(line), " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// Pi interactive-mode.ts:1854-1905: with showDiagnosticsWhenQuiet, quiet
// startup still renders [Skill conflicts], [Prompt conflicts], [Extension
// issues] (load errors, command diagnostics, built-in command conflicts, then
// shortcut diagnostics) and [Theme conflicts], in that order, through
// formatDiagnostics; the resource listing stays hidden.
func TestShowLoadedResourcesQuietDiagnosticsAllKinds(t *testing.T) {
	m := diagnosticsFixtureMode(t)
	m.opts.Settings.QuietStartup = QuietStartupTrue
	m.showLoadedResources(false, true)
	if got := renderListing(m); got != wantDiagnosticsBlock {
		t.Fatalf("quiet diagnostics =\n%s\nwant\n%s", got, wantDiagnosticsBlock)
	}
}

// Pi interactive-mode.ts:1698-1707: without showListing or
// showDiagnosticsWhenQuiet the container stays empty, diagnostics included.
func TestShowLoadedResourcesQuietWithoutDiagnosticsRequestShowsNothing(t *testing.T) {
	m := diagnosticsFixtureMode(t)
	m.opts.Settings.QuietStartup = QuietStartupTrue
	m.showLoadedResources(false, false)
	if got := renderListing(m); got != "" {
		t.Fatalf("quiet listing without the diagnostics request = %q, want empty", got)
	}
}

// A shown listing is followed by the same diagnostics block (Pi :1854).
func TestShowLoadedResourcesListingThenDiagnostics(t *testing.T) {
	m := diagnosticsFixtureMode(t)
	m.opts.Skills = []*SkillDef{{Path: "/s/a/SKILL.md", Name: "commit"}}
	m.showLoadedResources(false, false)
	got := renderListing(m)
	listing, _, ok := strings.Cut(got, "[Skill conflicts]")
	_, wantTail, _ := strings.Cut(wantDiagnosticsBlock, "[Prompt conflicts]")
	_, gotTail, _ := strings.Cut(got, "[Prompt conflicts]")
	if !ok || !strings.Contains(listing, "[Skills]") || gotTail != wantTail {
		t.Fatalf("listing plus diagnostics =\n%s", got)
	}
}

// Pi resource-loader.ts:869-1024 loadThemes and dedupeThemes: a missing path,
// a non-JSON file, an unparsable theme and a same-name theme each yield one
// diagnostic in load order, the first theme of a name wins, and every valid
// theme of a directory path registers.
func TestLoadThemeResourcesDiagnostics(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	text := filepath.Join(dir, "notes.txt")
	bad := filepath.Join(dir, "bad.json")
	for path, body := range map[string]string{text: "x", bad: "{"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	winner := writeNamedTheme(t, dir, "a-sunset.json", "sunset")
	loser := writeNamedTheme(t, dir, "b-sunset.json", "sunset")
	registry := tui.NewThemeRegistry()
	_, diagnostics := loadThemeResources(registry, []string{missing, text, bad, winner, loser}, tui.GetTerminalColorMode())
	if len(diagnostics) != 4 ||
		diagnostics[0] != (extension.ResourceDiagnostic{Type: extension.DiagnosticWarning, Message: "theme path does not exist", Path: missing}) ||
		diagnostics[1] != (extension.ResourceDiagnostic{Type: extension.DiagnosticWarning, Message: "theme path is not a json file", Path: text}) ||
		diagnostics[2] != (extension.ResourceDiagnostic{Type: extension.DiagnosticWarning, Path: bad, Message: "Failed to parse theme " + bad + ": SyntaxError: Expected property name or '}' in JSON at position 1 (line 1 column 2)"}) ||
		diagnostics[3].Type != extension.DiagnosticCollision || diagnostics[3].Message != `name "sunset" collision` || diagnostics[3].Path != loser ||
		diagnostics[3].Collision == nil || *diagnostics[3].Collision != (extension.ResourceCollision{ResourceType: "theme", Name: "sunset", WinnerPath: winner, LoserPath: loser}) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if got := registry.PathOf("sunset"); got != winner {
		t.Fatalf("registered sunset from %q, want the first theme %q", got, winner)
	}
}

// A directory path loads each .json theme; a broken one is a warning naming
// that file, not the directory (resource-loader.ts:911-947).
func TestLoadThemeResourcesDirectory(t *testing.T) {
	dir := t.TempDir()
	good := writeNamedTheme(t, dir, "good.json", "dusk")
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := tui.NewThemeRegistry()
	_, diagnostics := loadThemeResources(registry, []string{dir}, tui.GetTerminalColorMode())
	if len(diagnostics) != 1 || diagnostics[0].Path != bad || diagnostics[0].Type != extension.DiagnosticWarning {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if registry.PathOf("dusk") != good {
		t.Fatalf("dusk not registered from %q", good)
	}
}

// Pi loader.ts:570 returns the invalid-factory diagnostic without a Failed-to-load prefix.
func TestExtensionDiagnosticsRetainInvalidFactoryPath(t *testing.T) {
	m := upstreamListingMode(t, false, nil)
	m.opts.SubprocessHost = &reportingHost{report: &subprocess.ReloadReport{Issues: []string{
		"/ext/bad.ts: Extension does not export a valid factory function: /ext/bad.ts",
	}}}
	m.opts.Settings.QuietStartup = QuietStartupTrue
	m.showLoadedResources(false, true)
	want := "[Extension issues]\n  /ext/bad.ts\n    Extension does not export a valid factory function: /ext/bad.ts"
	if got := renderListing(m); got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
}

func writeNamedTheme(t *testing.T, dir, file, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["name"] = name
	if data, err = json.Marshal(document); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
