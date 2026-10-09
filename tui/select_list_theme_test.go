package tui

import "testing"

// theme.ts getSelectListTheme: selectedPrefix and selectedText are theme.fg("accent"), description, scrollInfo and noMatch are theme.fg("muted").
func TestGetSelectListThemeColorsFromTheActiveTheme(t *testing.T) {
	th := ActiveTheme()
	accent, muted := selectListColor(th.Accent, "\x1b[38;2;138;190;183m"), selectListColor(th.Muted, "\x1b[38;2;128;128;128m")
	theme := GetSelectListTheme()
	for name, tc := range map[string]struct {
		style func(string) string
		color string
	}{
		"selectedPrefix": {theme.SelectedPrefix, accent},
		"selectedText":   {theme.SelectedText, accent},
		"description":    {theme.Description, muted},
		"scrollInfo":     {theme.ScrollInfo, muted},
		"noMatch":        {theme.NoMatch, muted},
	} {
		if tc.style == nil {
			t.Fatalf("%s is unset", name)
		}
		if got, want := tc.style("x"), tc.color+"x"+FgClose(tc.color); got != want {
			t.Errorf("%s(x) = %q, want %q", name, got, want)
		}
	}
	if accent == muted {
		t.Fatal("accent and muted must differ for this test to tell the two apart")
	}
}
