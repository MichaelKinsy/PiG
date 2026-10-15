package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// settingsRowValue is the value the selector's row id shows.
func settingsRowValue(t *testing.T, list *tui.SettingsList, id string) string {
	t.Helper()
	for _, item := range list.Items() {
		if item.ID == id {
			return item.CurrentValue
		}
	}
	t.Fatalf("the settings selector has no %q row", id)
	return ""
}

// cycleSettingsRow selects row id and presses Enter until it shows target, as a user does, and returns the value the row shows afterwards.
func cycleSettingsRow(t *testing.T, list *tui.SettingsList, id, target string) string {
	t.Helper()
	list.SelectItem(id)
	for range 64 {
		if settingsRowValue(t, list, id) == target {
			return target
		}
		list.HandleInput("\r")
	}
	return settingsRowValue(t, list, id)
}

// useSettingsSelector installs a ShowSettingsSelector that builds the selector and hands its list to fn without a modal loop.
func useSettingsSelector(sc *SlashContext, fn func(selector *SettingsSelectorComponent)) *bool {
	shown := new(bool)
	sc.ShowSettingsSelector = func(build func(done func()) *SettingsSelectorComponent) {
		*shown = true
		fn(build(func() {}))
	}
	return shown
}
