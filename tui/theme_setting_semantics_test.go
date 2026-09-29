package tui

import "testing"

// Pi theme.ts:parseAutoThemeSetting trims only ECMAScript whitespace around each automatic theme name. Nonempty fixed settings are returned verbatim by resolveThemeSetting.
func TestThemeSettingECMAScriptWhitespace(t *testing.T) {
	for _, tc := range []struct {
		setting, light, dark string
		valid                bool
	}{
		{"light/dark", "light", "dark", true},
		{"\ufefflight / dark\ufeff", "light", "dark", true},
		{"\u0085light / dark\u0085", "\u0085light", "dark\u0085", true},
		{"\ufeff/dark", "", "", false},
		{"light/\ufeff", "", "", false},
		{"\u0085/dark", "\u0085", "dark", true},
		{"light/\u0085", "light", "\u0085", true},
		{"light/dark/extra", "", "", false},
	} {
		t.Run(tc.setting, func(t *testing.T) {
			light, dark, ok := ParseAutoThemeSetting(tc.setting)
			if ok != tc.valid || (ok && (light != tc.light || dark != tc.dark)) {
				t.Fatalf("parse(%q)=(%q,%q,%v), want=(%q,%q,%v)", tc.setting, light, dark, ok, tc.light, tc.dark, tc.valid)
			}
			for _, terminal := range []TerminalTheme{"light", "dark"} {
				want := tc.dark
				if terminal == "light" {
					want = tc.light
				}
				got, valid := ResolveThemeSetting(tc.setting, terminal)
				if valid != tc.valid || (valid && got != want) {
					t.Errorf("resolve(%q,%q)=(%q,%v), want=(%q,%v)", tc.setting, terminal, got, valid, want, tc.valid)
				}
			}
		})
	}
	for _, setting := range []string{" ", "\ufeffdark\ufeff", "\u0085", "dark"} {
		for _, terminal := range []TerminalTheme{"light", "dark"} {
			if got, ok := ResolveThemeSetting(setting, terminal); !ok || got != setting {
				t.Errorf("fixed resolve(%q,%q)=(%q,%v), want the supplied string", setting, terminal, got, ok)
			}
		}
	}
}

func TestThemeSettingBOMPairSelectsBuiltinByEnvironment(t *testing.T) {
	withTrueColor(t, true)
	t.Setenv("COLORFGBG", "0;15")
	SetTheme("dark")
	SetThemeSetting("\ufefflight / dark\ufeff")
	if got := ActiveTheme().Name; got != "light" {
		t.Fatalf("theme=%q, want light from the trimmed automatic setting", got)
	}
}
