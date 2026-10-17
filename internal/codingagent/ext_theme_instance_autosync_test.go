package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// theme-controller.ts:138-143 setThemeInstance: automatic selection stops, the active name is "<in-memory>", and nothing is persisted, so a
// terminal appearance change leaves the extension's theme object in place.
func TestExtensionThemeInstanceStopsAutomaticSelection(t *testing.T) {
	pinTrueColorCapabilities(t)
	f := themeControllerNew(t, "dark", nil)
	f.flush(t)
	instance := tui.ActiveThemeRegistry().Get("light")
	if instance == nil || tui.ActiveTheme() == instance {
		t.Fatalf("test needs a theme other than the active one: %v", instance)
	}
	f.m.theme().setAutoSync(true)
	ui := &ExtUIContext{m: f.m}
	if result := ui.SetTheme(extension.ThemeInstance{Theme: instance}); !result.Success {
		t.Fatalf("SetTheme(ThemeInstance) = %+v", result)
	}
	if got := f.m.opts.SettingsManager.Get().Theme; got != "dark" {
		t.Fatalf("persisted theme = %q: a theme object has no name to persist", got)
	}
	if active := f.m.themeState.activeThemeName.Load(); active == nil || *active != tui.InMemoryThemeName {
		t.Fatalf("active theme name = %v, want %q", active, tui.InMemoryThemeName)
	}
	if f.m.themeState.autoSyncEnabled.Load() {
		t.Fatal("automatic theme selection is still on after setTheme(Theme)")
	}
	f.m.theme().reapplyForTerminal()
	if tui.ActiveTheme() != instance {
		t.Fatal("a terminal appearance change replaced the extension's theme object")
	}
}
