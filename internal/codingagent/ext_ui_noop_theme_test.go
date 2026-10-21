package codingagent

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's no-op UI context answers `ctx.ui.theme` with the global theme (runner.ts noOpUIContext), so an in-process extension running with no UI (print, JSON) reads
// the palette the TUI would. This package registers the active theme with the shared no-op context at init.
func TestNoopUIContextThemeIsTheActiveTheme(t *testing.T) {
	previous := tui.ActiveTheme()
	t.Cleanup(func() { tui.SetThemeInstance(previous) })
	theme, err := tui.LoadThemeFromPath(filepath.Join("..", "..", "tui", "theme_dark.json"), tui.TerminalColorModeTrueColor)
	if err != nil {
		t.Fatal(err)
	}
	tui.SetThemeInstance(theme)

	// SetThemeInstance resolves the instance to the terminal's color depth, so the active theme is the reference, not the instance as loaded.
	got := extension.NoopUIContext.Theme()
	if got == nil {
		t.Fatal("NoopUIContext.Theme() = nil with an active theme, want its palette")
	}
	if want := ExtensionThemePalette(tui.ActiveTheme()); !reflect.DeepEqual(got, want) {
		t.Fatalf("NoopUIContext.Theme() = %+v, want the active theme's palette %+v", got, want)
	}
}
