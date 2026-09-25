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
