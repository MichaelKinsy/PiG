package host_test

// pi: apps/p2/test/p2/tui-theme.test.ts "launcher reads pi's files and p2 prefs, validates themes and hot-reloads custom themes" (the part up to
// the hot reload; the view built from the loaded preferences is in apps/p2/tui/tui_theme_test.go).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
)

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLauncherReadsPiFilesAndP2PrefsValidatesThemesAndHotReloadsCustomThemes(t *testing.T) {
	directory := t.TempDir()
	agentDir, configDir := filepath.Join(directory, "agent"), filepath.Join(directory, "p2-tui")
	if err := os.MkdirAll(filepath.Join(agentDir, "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := func(accent string) theme.ThemeJSON {
		document := theme.DarkTheme.WithColor("accent", theme.Str(accent))
		document.Name = "mine"
		return document
	}
	writeJSON(t, filepath.Join(agentDir, "themes", "mine.json"), custom("#123456"))
	writeJSON(t, filepath.Join(agentDir, "themes", "broken.json"), map[string]any{"name": "broken", "colors": map[string]any{}})
	writeJSON(t, filepath.Join(agentDir, "themes", "dark.json"), theme.DarkTheme)
	writeJSON(t, filepath.Join(agentDir, "settings.json"), map[string]any{
		"theme": "dark", "defaultModel": "secret-ish", "markdown": map[string]any{"codeBlockIndent": "    "}, "quietStartup": 3,
	})
	writeJSON(t, filepath.Join(configDir, "prefs.json"), map[string]any{"theme": "mine", "settings": map[string]any{"quietStartup": true}})

	sources := host.LocalPreferenceSources{AgentDir: agentDir, ConfigDir: configDir}
	loaded := host.LoadLocalPreferences(sources)
	if loaded.Theme != "mine" {
		t.Fatalf("theme = %q, want mine", loaded.Theme)
	}
	names := make([]string, 0, len(loaded.Themes))
	for name := range loaded.Themes {
		names = append(names, name)
	}
	if !slices.Equal(names, []string{"mine"}) {
		t.Fatalf("themes = %v", names)
	}
	indent := "    "
	wantSettings := prefs.TuiSettings{Markdown: &prefs.MarkdownSettings{CodeBlockIndent: &indent}, QuietStartup: &prefs.QuietStartup{On: true}}
	if !reflect.DeepEqual(loaded.Settings, wantSettings) {
		t.Fatalf("settings = %+v, want %+v", loaded.Settings, wantSettings)
	}
	if !slices.ContainsFunc(loaded.Problems, func(problem string) bool { return strings.HasPrefix(problem, "themes/broken.json: Invalid theme") }) {
		t.Fatalf("problems lack the broken theme: %q", loaded.Problems)
	}
	for _, want := range []string{`themes/dark.json: theme name "dark" is reserved or invalid`, "settings.json: invalid quietStartup"} {
		if !slices.Contains(loaded.Problems, want) {
			t.Fatalf("problems lack %q: %q", want, loaded.Problems)
		}
	}
	encoded, _ := json.Marshal(loaded)
	if strings.Contains(string(encoded), directory) {
		t.Fatal("local paths reach the TUI")
	}
	if strings.Contains(string(encoded), "secret-ish") {
		t.Fatal("settings other than the whitelisted ones reach the TUI")
	}
	themed := sources
	themed.Theme, themed.NoThemes = "light", true
	if got := host.LoadLocalPreferences(themed).Theme; got != "light" {
		t.Fatalf("--theme light = %q", got)
	}
	noThemes := sources
	noThemes.NoThemes = true
	if got := host.LoadLocalPreferences(noThemes).Themes; len(got) != 0 {
		t.Fatalf("--no-themes loaded %v", got)
	}

	var mu sync.Mutex
	var updates []prefs.TuiLocalPreferences
	stop := host.WatchLocalPreferences(sources, func(next prefs.TuiLocalPreferences) {
		mu.Lock()
		defer mu.Unlock()
		updates = append(updates, next)
	}, loaded)
	t.Cleanup(stop)
	writeJSON(t, filepath.Join(agentDir, "themes", "mine.json"), custom("#654321"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		found := slices.ContainsFunc(updates, func(next prefs.TuiLocalPreferences) bool {
			encoded, _ := json.Marshal(next.Themes["mine"].Colors)
			return strings.Contains(string(encoded), "#654321")
		})
		mu.Unlock()
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the edited custom theme was not reloaded within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
