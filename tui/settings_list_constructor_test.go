package tui

import (
	"strings"
	"testing"
)

func constructorItems() []SettingItem {
	return []SettingItem{
		{ID: "mode", Label: "Mode", CurrentValue: "one", Values: []string{"one", "two"}},
		{ID: "other", Label: "Other", CurrentValue: "off", Values: []string{"off", "on"}},
	}
}

func markedSettingsTheme() SettingsListTheme {
	return SettingsListTheme{
		Label:       func(text string, selected bool) string { return "L(" + text },
		Value:       func(text string, selected bool) string { return "V(" + text + ")" },
		Description: func(text string) string { return "D(" + text + ")" },
		Cursor:      ">> ",
		Hint:        func(text string) string { return "H(" + text + ")" },
	}
}

// packages/tui/src/components/settings-list.ts:55-73,185-222: the constructor's theme styles every part, and onChange runs with the item id
// and the new value after the row cycles.
func TestNewSettingsListUsesItsThemeAndOnChange(t *testing.T) {
	var gotID, gotValue string
	list := NewSettingsList(constructorItems(), 5, markedSettingsTheme(), func(id, value string) { gotID, gotValue = id, value }, func() {})
	text := strings.Join(list.Render(60), "\n")
	for _, want := range []string{">> ", "L(Mode", "V(one)", "H("} {
		if !strings.Contains(text, want) {
			t.Fatalf("render lacks %q from the constructor theme:\n%s", want, text)
		}
	}
	list.HandleInput("\r")
	if gotID != "mode" || gotValue != "two" {
		t.Fatalf("onChange = %q/%q, want mode/two", gotID, gotValue)
	}
}

// settings-list.ts:290-296,63-68: the cancel key calls onCancel.
func TestNewSettingsListCancelCallsOnCancel(t *testing.T) {
	cancelled := 0
	list := NewSettingsList(constructorItems(), 5, markedSettingsTheme(), func(string, string) {}, func() { cancelled++ })
	list.HandleInput("\x1b")
	if cancelled != 1 {
		t.Fatalf("onCancel ran %d times, want 1", cancelled)
	}
}

// settings-list.ts:61,69-72: `options = {}` and `enableSearch ?? false`, so search is off unless the option is true.
func TestNewSettingsListSearchFollowsOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []SettingsListOptions
		want    bool
	}{{"no options", nil, false}, {"absent enableSearch", []SettingsListOptions{{}}, false}, {"true", []SettingsListOptions{{EnableSearch: true}}, true}} {
		list := NewSettingsList(constructorItems(), 5, markedSettingsTheme(), nil, nil, tc.options...)
		if (list.searchInput != nil) != tc.want || list.searchEnabled != tc.want {
			t.Errorf("%s: search enabled = %v (input %v), want %v", tc.name, list.searchEnabled, list.searchInput != nil, tc.want)
		}
	}
}
