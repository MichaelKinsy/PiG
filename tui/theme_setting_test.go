package tui

import "testing"

func TestParseAutoThemeSetting(t *testing.T) {
	light, dark, ok := ParseAutoThemeSetting(" light-theme / dark-theme ")
	if !ok || light != "light-theme" || dark != "dark-theme" {
		t.Fatalf("ParseAutoThemeSetting valid = (%q, %q, %v), want light-theme/dark-theme true", light, dark, ok)
	}

	for _, setting := range []string{"", "dark", "light/", "/dark", "light/dark/extra"} {
		if _, _, ok := ParseAutoThemeSetting(setting); ok {
			t.Fatalf("ParseAutoThemeSetting(%q) ok=true, want false", setting)
		}
	}
}

func TestResolveThemeSetting(t *testing.T) {
	if got, ok := ResolveThemeSetting("solarized-light/solarized-dark", TerminalTheme("light")); !ok || got != "solarized-light" {
		t.Fatalf("ResolveThemeSetting light = (%q, %v), want solarized-light true", got, ok)
	}
	if got, ok := ResolveThemeSetting("solarized-light/solarized-dark", TerminalTheme("dark")); !ok || got != "solarized-dark" {
		t.Fatalf("ResolveThemeSetting dark = (%q, %v), want solarized-dark true", got, ok)
	}
	if got, ok := ResolveThemeSetting("dark", TerminalTheme("light")); !ok || got != "dark" {
		t.Fatalf("ResolveThemeSetting fixed = (%q, %v), want dark true", got, ok)
	}
	if got, ok := ResolveThemeSetting("light/dark/extra", TerminalTheme("dark")); ok || got != "" {
		t.Fatalf("ResolveThemeSetting malformed = (%q, %v), want empty false", got, ok)
	}
}

// Upstream 0.99.1 theme.ts resolveThemeSetting returns undefined only for an undefined or malformed slash setting; typeof "" is "string", so the empty name resolves to itself.
func TestResolveThemeSettingPresence(t *testing.T) {
	for _, terminal := range []TerminalTheme{"light", "dark"} {
		if got, ok := ResolveThemeSettingPresence(nil, terminal); ok || got != "" {
			t.Errorf("ResolveThemeSettingPresence(nil, %s) = (%q, %v), want nothing", terminal, got, ok)
		}
		if got, ok := ResolveThemeSettingPresence(new(""), terminal); !ok || got != "" {
			t.Errorf("ResolveThemeSettingPresence(\"\", %s) = (%q, %v), want the empty name", terminal, got, ok)
		}
	}
}

// Upstream 0.99.1 interactive-mode.ts initTheme applies resolveThemeSetting(setting, terminalTheme) ?? "system", and theme.ts setTheme falls back to the system theme for a name it cannot load.
func TestSetThemeSettingPresence(t *testing.T) {
	withTrueColor(t, true)
	t.Setenv("COLORFGBG", "0;15")
	previous := ActiveTheme().Name
	t.Cleanup(func() { SetThemeByName(previous) })
	for _, tc := range []struct {
		name    string
		setting *string
		want    string
	}{
		{"unset selects the system theme", nil, "system"},
		{"malformed selects the system theme", new("light/dark/extra"), "system"},
		{"empty name falls back to system", new(""), "system"},
		{"unknown name falls back to system", new("no-such-theme"), "system"},
		{"automatic pair follows the environment", new("light/dark"), "light"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetTheme("dark")
			SetThemeSettingPresence(tc.setting)
			if got := ActiveTheme().Name; got != tc.want {
				t.Fatalf("theme = %q, want %q", got, tc.want)
			}
		})
	}
}

// The deprecated string forms keep their contract: an empty string means no setting.
func TestDeprecatedThemeSettingStringForms(t *testing.T) {
	withTrueColor(t, true)
	t.Setenv("COLORFGBG", "0;15")
	previous := ActiveTheme().Name
	t.Cleanup(func() { SetThemeByName(previous) })
	for _, terminal := range []TerminalTheme{"light", "dark"} {
		if got, ok := ResolveThemeSetting("", terminal); ok || got != "" {
			t.Errorf("ResolveThemeSetting(\"\", %s) = (%q, %v), want no setting", terminal, got, ok)
		}
		if got, ok := ResolveThemeSetting("light/dark", terminal); !ok || got != string(terminal) {
			t.Errorf("ResolveThemeSetting(\"light/dark\", %s) = (%q, %v), want %s", terminal, got, ok, terminal)
		}
	}
	SetTheme("dark")
	SetThemeSetting("")
	if got := ActiveTheme().Name; got != "system" {
		t.Fatalf("SetThemeSetting(\"\") theme = %q, want the system theme", got)
	}
	SetThemeSetting("no-such-theme")
	if got := ActiveTheme().Name; got != "system" {
		t.Fatalf("SetThemeSetting(unknown) theme = %q, want system", got)
	}
}
