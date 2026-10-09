package tui

import (
	"strings"
	"testing"
)

// Ports settings-list.ts SettingsListTheme: every styled part of the list goes through the injected theme: cursor
// prefix, label, value, description and hint (line 140-169, 319).
func TestSettingsListUsesInjectedTheme(t *testing.T) {
	theme := SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return "<L*" + text + ">"
			}
			return "<L" + text + ">"
		},
		Value: func(text string, selected bool) string {
			if selected {
				return "<V*" + text + ">"
			}
			return "<V" + text + ">"
		},
		Description: func(text string) string { return "<D" + text + ">" },
		Cursor:      ">> ",
		Hint:        func(text string) string { return "<H" + text + ">" },
	}
	items := []SettingItem{
		{ID: "a", Label: "alpha", CurrentValue: "on", Description: "first setting"},
		{ID: "b", Label: "beta", CurrentValue: "off"},
	}
	list := NewSettingsList(items, 10, theme, nil, nil, SettingsListOptions{EnableSearch: false})
	rendered := strings.Join(list.Render(60), "\n")
	for _, want := range []string{">> <L*alpha>", "<V*on>", "<Lbeta >", "<Voff>", "<D  first setting>", "<H  Enter/Space to change · Esc to cancel>"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("injected theme part %q missing from %q", want, rendered)
		}
	}

	empty := strings.Join(NewSettingsList(nil, 10, theme, nil, nil, SettingsListOptions{EnableSearch: true}).Render(60), "\n")
	if !strings.Contains(empty, "<H  No settings available>") {
		t.Errorf("empty state ignores the injected hint style: %q", empty)
	}
	if plain := strings.Join(NewSettingsList(items, 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: false}).Render(60), "\n"); strings.Contains(plain, "<L") {
		t.Errorf("default list leaked the injected theme: %q", plain)
	}
}

// theme.ts getSettingsListTheme is what the settings menus pass: its hint is the dim token, closed with SGR 39 (TestSettingsListThemeMatchesPi compares every part with Pi).
func TestGetSettingsListThemeHintIsTheDimStyle(t *testing.T) {
	if hint := GetSettingsListTheme().Hint("x"); !strings.HasPrefix(hint, ActiveTheme().Dim) || !strings.HasSuffix(hint, "\x1b[39m") {
		t.Errorf("hint = %q is not the dim style", hint)
	}
}
