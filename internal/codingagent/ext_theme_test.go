package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// TestExtensionThemeAPIsAreWired pins the three theme APIs an extension calls.
// All three were stubs: GetAllThemes returned nil, GetTheme errored with
// "not yet implemented", and SetTheme refused every name, so the themes a user
// has installed were invisible and unreachable from any extension.
func TestExtensionThemeAPIsAreWired(t *testing.T) {
	restore := tui.ActiveThemeRegistry()
	t.Cleanup(func() { tui.SetThemeRegistry(restore) })
	tui.SetThemeRegistry(tui.NewThemeRegistry())

	ui := &ExtUIContext{m: newThemeTestMode(t)}

	themes := ui.GetAllThemes()
	if len(themes) == 0 {
		t.Fatal("GetAllThemes returned nothing; the built-in themes are always present")
	}
	var haveDark bool
	for _, meta := range themes {
		if meta.Name == "dark" {
			haveDark = true
		}
		if meta.Name == "" {
			t.Error("GetAllThemes returned a theme with an empty name")
		}
	}
	if !haveDark {
		t.Errorf("GetAllThemes = %+v, want the built-in dark theme listed", themes)
	}

	if _, err := ui.GetTheme("dark"); err != nil {
		t.Errorf("GetTheme(dark) error = %v, want the built-in theme", err)
	}
	if _, err := ui.GetTheme("no-such-theme"); err == nil {
		t.Error("GetTheme(no-such-theme) error = nil, want an unknown-theme error")
	}

	if got := ui.SetTheme("light"); !got.Success {
		t.Errorf("SetTheme(light) = %+v, want success", got)
	}
	if got := tui.ActiveTheme().Name; got != "light" {
		t.Errorf("active theme = %q after SetTheme(light), want light", got)
	}

	// An unknown name must be refused rather than silently applied, otherwise
	// the UI would switch to whatever fallback SetThemeSetting picks.
	before := tui.ActiveTheme().Name
	if got := ui.SetTheme("no-such-theme"); got.Success {
		t.Error("SetTheme(no-such-theme) succeeded; unknown themes must be refused")
	}
	if after := tui.ActiveTheme().Name; after != before {
		t.Errorf("refused SetTheme still changed the theme: %q → %q", before, after)
	}

	if got := ui.SetTheme(42); got.Success {
		t.Error("SetTheme(42) succeeded; only a theme name is accepted")
	}
}

// newThemeTestMode builds the minimum InteractiveMode the theme APIs touch:
// settings for persistence, and no TUI instance so rendering is skipped.
func newThemeTestMode(t *testing.T) *InteractiveMode {
	t.Helper()
	return &InteractiveMode{opts: InteractiveOptions{Settings: Settings{Theme: "dark"}}}
}
