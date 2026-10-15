package codingagent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi binds the session's extensions and calls showLoadedResources with
// showDiagnosticsWhenQuiet (interactive-mode.ts:1984). Under quietStartup the
// startup bind must therefore list, and only list, prompt conflicts, extension
// issues (built-in command conflicts) and theme warnings, in Pi's order.
func TestQuietStartupBindShowsDiagnosticsOnly(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a", "same.md"), filepath.Join(dir, "b", "same.md")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("body"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	missingTheme := filepath.Join(dir, "missing-theme.json")
	old := tui.ActiveThemeRegistry()
	t.Cleanup(func() { tui.SetThemeRegistry(old) })
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	f := newRebindFixture(t, func(*coding.Session, ...any) (any, error) { return nil, nil }, nil, func(o *icodingagent.InteractiveModeOptions) {
		o.PromptPaths = []string{first, second}
		o.ThemePaths = []string{missingTheme}
	})
	sm := f.runtime.Services().SettingsManager()
	if err := sm.SetQuietStartup(coding.QuietStartupTrue); err != nil {
		t.Fatal(err)
	}
	f.h.SetRebindResources(sm, f.runtime.Services().Registry().ModelRegistry)
	f.h.LoadStartupResources()
	runner := inproc.NewRunner([]extension.Extension{{
		Path:         "/ext/cmd.ts",
		Commands:     map[string]extension.RegisteredCommand{"model": {Name: "model", SourceInfo: icodingagent.PiSourceInfo{Path: "/ext/cmd.ts"}}},
		CommandOrder: []string{"model"},
	}}, f.runtime.Session().Inner().GetCwd())
	if err := f.h.RebindSession(t.Context(), f.runtime.Session(), runner, nil, false); err != nil {
		t.Fatal(err)
	}
	got := f.h.LoadedResources()
	prompt := strings.Index(got, "[Prompt conflicts]")
	ext := strings.Index(got, "[Extension issues]")
	theme := strings.Index(got, "theme path does not exist")
	if prompt < 0 || ext < prompt || theme < ext || !strings.Contains(got, "Extension command '/model' conflicts with built-in interactive command. Skipping in autocomplete.") {
		t.Fatalf("quiet startup diagnostics missing or misordered:\n%s", got)
	}
	if strings.Contains(got, "[Context]") || strings.Contains(got, "[Prompts]") {
		t.Fatalf("quiet startup showed the resource listing:\n%s", got)
	}
}
