package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// themeRendererSpy counts the repaints the theme change requests.
type themeRendererSpy struct {
	tui.Renderer
	invalidates, requests int
}

func (s *themeRendererSpy) Invalidate()    { s.invalidates++ }
func (s *themeRendererSpy) RequestRender() { s.requests++ }

func themeSettingsMode(t *testing.T) (*ExtUIContext, *SettingsManager, string, *themeRendererSpy) {
	t.Helper()
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	tui.SetThemeByName("dark", false)
	agentDir := t.TempDir()
	sm := NewSettingsManager(t.TempDir(), agentDir)
	if err := sm.UpdateGlobal(func(settings *Settings) { settings.Theme = "dark" }); err != nil {
		t.Fatal(err)
	}
	m := newThemeTestMode(t)
	m.opts.SettingsManager = sm
	renderer := &themeRendererSpy{}
	m.tuiInst = renderer
	return &ExtUIContext{m: m}, sm, filepath.Join(agentDir, "settings.json"), renderer
}

// Ports packages/coding-agent/test/interactive-mode-status.test.ts:171-200 (persists theme changes to settings manager). The production controller path replaces the mocked setThemeName; upstream's requestRender-once assertion becomes one Invalidate and one RequestRender.
func TestExtensionSetThemePersistsToSettingsManagerUpstream(t *testing.T) {
	ui, sm, path, renderer := themeSettingsMode(t)
	if got := sm.GetTheme(); got != "dark" {
		t.Fatalf("initial stored theme=%q, want dark", got)
	}
	result := ui.SetTheme("light")
	if !result.Success {
		t.Fatalf("SetTheme(light)=%+v", result)
	}
	if got := sm.GetTheme(); got != "light" {
		t.Fatalf("stored theme=%q, want light", got)
	}
	if got := tui.ActiveTheme().Name; got != "light" {
		t.Fatalf("active theme=%q, want light", got)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"theme": "light"`) {
		t.Fatalf("settings file %q err=%v does not persist the theme", data, err)
	}
	if renderer.invalidates != 1 || renderer.requests != 1 {
		t.Fatalf("invalidates=%d render requests=%d, want one each", renderer.invalidates, renderer.requests)
	}
}

// Ports packages/coding-agent/test/interactive-mode-status.test.ts:204-227 (does not persist invalid theme names). Upstream's mocked controller asserts requestRender is not called, which only shows that the UI-context wrapper adds no render. Pi's real controller repaints once while falling back to the system theme (upstream 0.99.1 theme.ts:772-790 setTheme, theme-controller.ts:178-186 applyThemeName -> notifyChanged), so the faithful counterpart is exactly one Invalidate and one RequestRender. The test starts from light so the system fallback proves the name was applied.
func TestExtensionSetThemeDoesNotPersistInvalidNamesUpstream(t *testing.T) {
	ui, sm, path, renderer := themeSettingsMode(t)
	tui.SetThemeByName("light", false)
	result := ui.SetTheme("__missing_theme__")
	if result.Success || result.Error != "Theme not found: __missing_theme__" {
		t.Fatalf("SetTheme(__missing_theme__)=%+v", result)
	}
	if got := sm.GetTheme(); got != "dark" {
		t.Fatalf("stored theme=%q, want dark", got)
	}
	if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), "__missing_theme__") {
		t.Fatalf("invalid theme reached settings: %s", data)
	}
	if got := tui.ActiveTheme().Name; got != tui.SystemThemeName {
		t.Fatalf("fallback theme=%q, want system after starting from light", got)
	}
	if renderer.invalidates != 1 || renderer.requests != 1 {
		t.Fatalf("invalidates=%d render requests=%d, want one each", renderer.invalidates, renderer.requests)
	}
}
