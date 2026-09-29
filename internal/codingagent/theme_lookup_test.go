package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi theme.ts:570-575 returns a loaded Theme without selecting it and returns undefined for missing names.
func TestExtensionThemeLookupUsesPortablePaletteAndAbsence(t *testing.T) {
	registry, active := tui.ActiveThemeRegistry(), tui.ActiveTheme().Name
	t.Cleanup(func() { tui.SetThemeRegistry(registry); tui.SetThemeByName(active) })
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	tui.SetThemeByName("dark")
	ui := &ExtUIContext{m: newThemeTestMode(t)}
	value, err := ui.GetTheme("light")
	if err != nil {
		t.Fatal(err)
	}
	palette, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("lookup returned %T, not the shared extension palette", value)
	}
	foregrounds, _ := palette["foregrounds"].(map[string]string)
	if palette["name"] != "light" || foregrounds["accent"] == "" {
		t.Fatalf("incomplete palette: %#v", palette)
	}
	if tui.ActiveTheme().Name != "dark" {
		t.Fatal("lookup selected the theme")
	}
	if missing, err := ui.GetTheme("missing-rigidity-theme"); missing != nil || err != nil {
		t.Fatalf("missing theme = %#v, %v; want absence", missing, err)
	}
}
