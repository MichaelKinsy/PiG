package codingagent

import (
	"testing"
)

// showSettingsSelector (interactive-mode.ts:4869-5018) saves every cycled row through its SettingsManager setter. Each Enter on a cycling row must reach settings.json: a fresh manager on the same directory shows the new value in a fresh selector.
func TestSettingsSelectorCyclingRowsPersistThroughTheirSetters(t *testing.T) {
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	sc := &SlashContext{Append: func(string) {}, SettingsManager: sm}
	var cycled []string
	shown := map[string]string{}
	useSettingsSelector(sc, func(selector *SettingsSelectorComponent) {
		list := selector.GetSettingsList()
		for _, item := range list.Items() {
			// tui-mode saves only after the renderer switches, which needs the interactive host.
			if item.Submenu != nil || len(item.Values) < 2 || item.ID == "tui-mode" {
				continue
			}
			before := settingsRowValue(t, list, item.ID)
			list.SelectItem(item.ID)
			list.HandleInput("\r")
			after := settingsRowValue(t, list, item.ID)
			if after == before {
				t.Fatalf("Enter did not cycle %s from %q", item.ID, before)
			}
			cycled = append(cycled, item.ID)
			shown[item.ID] = after
		}
	})
	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	if len(cycled) < 20 {
		t.Fatalf("cycled only %d rows: %v", len(cycled), cycled)
	}
	reloaded := &SlashContext{Append: func(string) {}, SettingsManager: NewSettingsManager(dir, dir)}
	useSettingsSelector(reloaded, func(selector *SettingsSelectorComponent) {
		for _, id := range cycled {
			if got := settingsRowValue(t, selector.GetSettingsList(), id); got != shown[id] {
				t.Errorf("%s: settings.json holds %q, the selector showed %q", id, got, shown[id])
			}
		}
	})
	if err := settingsHandler(reloaded); err != nil {
		t.Fatal(err)
	}
}
