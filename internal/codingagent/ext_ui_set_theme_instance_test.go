package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// theme-controller.ts setThemeInstance and interactive-mode.ts:2670-2673: setTheme given a Theme object applies it as it is, stops automatic selection, does not persist a name, and leaves it in place when the terminal's appearance changes.
func TestExtensionSetThemeAcceptsAThemeObject(t *testing.T) {
	pinTrueColorCapabilities(t)
	f := themeControllerNew(t, "dark", nil)
	f.flush(t)
	instance := tui.ActiveThemeRegistry().Get("light")
	if instance == nil || tui.ActiveTheme() == instance {
		t.Fatalf("test needs a theme other than the active one: %v", instance)
	}
	ui := &ExtUIContext{m: f.m}
	if result := ui.SetTheme(extension.ThemeInstance{Theme: instance}); !result.Success {
		t.Fatalf("SetTheme(Theme) = %+v", result)
	}
	if tui.ActiveTheme() != instance {
		t.Fatalf("active theme = %q, want the instance %q", tui.ActiveTheme().Name, instance.Name)
	}
	if got := f.m.opts.SettingsManager.Get().Theme; got != "dark" {
		t.Fatalf("persisted theme = %q: a theme object has no name to persist", got)
	}
	if active := f.m.themeState.activeThemeName.Load(); active == nil || *active != tui.InMemoryThemeName {
		t.Fatalf("active theme name = %v, want %q", active, tui.InMemoryThemeName)
	}
	// Themes set through extensions are left alone when the terminal reports a new appearance.
	f.m.theme().reapplyForTerminal()
	if tui.ActiveTheme() != instance {
		t.Fatal("a terminal appearance change replaced the theme instance")
	}
	// A name still works afterwards.
	if result := ui.SetTheme(extension.ThemeName("dark")); !result.Success || tui.ActiveTheme() == instance {
		t.Fatalf("SetTheme(name) after an instance = %+v", result)
	}
	if result := ui.SetTheme(extension.ThemeInstance{}); result.Success {
		t.Fatal("SetTheme(empty ThemeInstance) succeeded")
	}
}
