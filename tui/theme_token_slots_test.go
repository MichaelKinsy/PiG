package tui

import "testing"

// theme.ts:117 ThemeToken = ThemeColor | ThemeBg: a foreground token colors text only through fg and a background token only through bg; the other slot throws "Unknown theme color".
func TestThemeTokenIsAcceptedOnlyInItsOwnSlot(t *testing.T) {
	theme := ActiveTheme()
	fgToken, bgToken := ThemeToken(ThemeColorAccent), ThemeToken(ThemeBgSelectedBg)
	if got := theme.Fg(string(fgToken), "x"); got == "" {
		t.Fatal("a foreground token must style through fg")
	}
	if got := theme.Bg(string(bgToken), "x"); got == "" {
		t.Fatal("a background token must style through bg")
	}
	for name, use := range map[string]func(){
		"background token through fg": func() { theme.Fg(string(bgToken), "x") },
		"foreground token through bg": func() { theme.Bg(string(fgToken), "x") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s did not throw", name)
				}
			}()
			use()
		}()
	}
}
