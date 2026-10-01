package tui

import "testing"

// Pi 0.99.2 theme.ts:903-906 getResolvedThemeColors and :919-926 getThemeExportColors load
// `themeName ?? currentThemeName ?? SYSTEM_THEME_NAME`, and currentThemeName is only set by initTheme/setTheme
// (theme.ts:756-797), which main.ts runs after the --export branch (main.ts:633 versus :898).
func TestExportThemeIsSystemUntilAThemeIsSelected(t *testing.T) {
	t.Cleanup(func() {
		selectedThemeName.Store(nil)
		activeTheme.Store(nil)
	})
	selectedThemeName.Store(nil)
	activeTheme.Store(nil)

	got := ExportTheme()
	if got == nil {
		t.Fatal("ExportTheme() = nil before any selection, want the system theme")
	}
	if got.Name != SystemThemeName {
		t.Fatalf("ExportTheme().Name = %q before any selection, want %q", got.Name, SystemThemeName)
	}
	// The system theme without terminal colors maps its tokens to the terminal's 16-color palette, which Pi exports as
	// the xterm palette hex values: #800080 is palette color 5 (observed in `pi --export` 0.99.2).
	if accent := got.Colors()["accent"]; accent != "#800080" {
		t.Fatalf("system theme --accent = %q, want #800080", accent)
	}
	if activeTheme.Load() != nil || selectedThemeName.Load() != nil {
		t.Fatal("ExportTheme() must not select the theme it loads")
	}

	// A call to ActiveTheme (the dark fallback for render paths) is not a selection.
	_ = ActiveTheme()
	if got := ExportTheme(); got.Name != SystemThemeName {
		t.Fatalf("ExportTheme() after ActiveTheme() = %q, want %q", got.Name, SystemThemeName)
	}
}

func TestExportThemeIsTheSelectedTheme(t *testing.T) {
	t.Cleanup(func() {
		selectedThemeName.Store(nil)
		activeTheme.Store(nil)
	})
	for _, name := range []string{"dark", "light"} {
		SetTheme(name)
		if got := ExportTheme(); got == nil || got != ActiveTheme() || got.Name != name {
			t.Fatalf("after SetTheme(%q): ExportTheme() = %v, want the active %q theme", name, got, name)
		}
	}
	SetThemeByName("no-such-theme")
	if got := ExportTheme(); got == nil || got.Name != SystemThemeName {
		t.Fatalf("after selecting a missing theme ExportTheme() = %v, want the system theme (theme.ts:780-786)", got)
	}
}
