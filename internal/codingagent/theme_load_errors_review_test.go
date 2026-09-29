package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi resource-loader.ts:886-890 tests existsSync, which is false for every stat failure, so a path below a regular file is reported as missing.
func TestLoadThemeResourcesUnstatablePathDoesNotExist(t *testing.T) {
	file := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(file, "theme.json")
	_, diagnostics := loadThemeResources(tui.NewThemeRegistry(), []string{path})
	want := extension.ResourceDiagnostic{Type: extension.DiagnosticWarning, Message: "theme path does not exist", Path: path}
	if len(diagnostics) != 1 || diagnostics[0] != want {
		t.Fatalf("diagnostics = %#v, want %#v", diagnostics, want)
	}
}

// Pi resource-loader.ts:911-947 reports readdirSync and readFileSync failures with Node's fs error message.
func TestLoadThemeResourcesNodeFSErrorText(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("POSIX permission bits are required to make a path unreadable")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "locked")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := writeNamedTheme(t, root, "locked.json", "locked")
	for _, path := range []string{dir, file} {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
	}
	_, diagnostics := loadThemeResources(tui.NewThemeRegistry(), []string{dir, file})
	want := []extension.ResourceDiagnostic{
		{Type: extension.DiagnosticWarning, Message: "EACCES: permission denied, scandir '" + dir + "'", Path: dir},
		{Type: extension.DiagnosticWarning, Message: "EACCES: permission denied, open '" + file + "'", Path: file},
	}
	if len(diagnostics) != len(want) || diagnostics[0] != want[0] || diagnostics[1] != want[1] {
		t.Fatalf("diagnostics = %#v, want %#v", diagnostics, want)
	}
}

// Pi getAvailableThemesWithPaths (theme.ts:426-478) lists every valid named theme in the custom themes directory, and loadThemeJson (theme.ts:506-526) loads it, even when resource settings or --no-themes leave it unregistered.
// Those themes are selectable without becoming loaded resources or diagnostics.
func TestLoadThemesKeepsCustomDirectoryThemesSelectable(t *testing.T) {
	old := tui.ActiveThemeRegistry()
	t.Cleanup(func() { tui.SetThemeRegistry(old) })
	agentDir := t.TempDir()
	themesDir := filepath.Join(agentDir, "themes")
	if err := os.Mkdir(themesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	excluded := writeNamedTheme(t, themesDir, "excluded.json", "excluded")
	writeNamedTheme(t, themesDir, "dark-copy.json", "dark")
	if err := os.WriteFile(filepath.Join(themesDir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	explicitDir := t.TempDir()
	explicit := writeNamedTheme(t, explicitDir, "excluded.json", "excluded")
	for _, tc := range []struct {
		name       string
		noThemes   bool
		themePaths []string
		wantPath   string
	}{
		{"excluded by settings", false, nil, excluded},
		{"no-themes", true, nil, excluded},
		{"resolved theme wins", false, []string{explicit}, explicit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &InteractiveMode{opts: InteractiveOptions{AgentDir: agentDir, NoThemes: tc.noThemes, ThemePaths: tc.themePaths}}
			m.loadThemes()
			registry := tui.ActiveThemeRegistry()
			if got := registry.PathOf("excluded"); got != tc.wantPath {
				t.Fatalf("excluded theme path = %q, want %q", got, tc.wantPath)
			}
			if got := registry.PathOf("dark"); got != "" {
				t.Fatalf("custom theme replaced built-in dark from %q", got)
			}
			if len(m.loadedThemes) != len(tc.themePaths) || len(m.themeDiagnostics) != 0 {
				t.Fatalf("loaded themes = %#v, diagnostics = %#v", m.loadedThemes, m.themeDiagnostics)
			}
		})
	}
}
