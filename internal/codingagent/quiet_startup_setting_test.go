package codingagent

import (
	"encoding/json"
	"testing"
)

// settings-manager.ts:111-112 and 1089-1098 (Pi 1.0.0): quietStartup is true, "header" or false. getQuietStartup keeps true and "header" and reads every other value as false, and a project value replaces the global one even when it is null or invalid (deepMergeObjects skips only undefined). Pi has no test for these reads; the expectations are a probe of the pinned Pi 1.0.0 SettingsManager.create over the same global and project files: true, "header", false, false, "header", false, false, false, false, and setQuietStartup("header") writes {"quietStartup": "header"}.
func TestQuietStartupSettingReadsAsPi(t *testing.T) {
	for _, tc := range []struct {
		global, project string
		want            QuietStartup
	}{
		{`{"quietStartup":true}`, `{}`, QuietStartupTrue},
		{`{"quietStartup":"header"}`, `{}`, QuietStartupHeader},
		{`{"quietStartup":"foo"}`, `{}`, QuietStartupFalse},
		{`{"quietStartup":1}`, `{}`, QuietStartupFalse},
		{`{"quietStartup":true}`, `{"quietStartup":"header"}`, QuietStartupHeader},
		{`{"quietStartup":true}`, `{"quietStartup":null}`, QuietStartupFalse},
		{`{"quietStartup":true}`, `{"quietStartup":"foo"}`, QuietStartupFalse},
		{`{"quietStartup":"header"}`, `{"quietStartup":false}`, QuietStartupFalse},
		{`{}`, `{}`, QuietStartupFalse},
	} {
		if got := writeSettingsLayers(t, tc.global, tc.project).GetQuietStartup(); got != tc.want {
			t.Errorf("global %s, project %s: GetQuietStartup() = %v, want %v", tc.global, tc.project, got, tc.want)
		}
	}

	sm := writeSettingsLayers(t, `{}`, `{}`)
	settingsOK(t, sm.SetQuietStartup(QuietStartupHeader))
	settingsOK(t, sm.Flush())
	assertSettingsFileJSON(t, sm.GlobalPath(), `{"quietStartup":"header"}`)
	settingsEqual(t, NewSettingsManager(sm.CWD(), sm.AgentDir()).GetQuietStartup(), QuietStartupHeader)
}

// An unrelated setting change leaves an authored quietStartup value as written, as Pi's save writes only modified fields (settings-manager.ts markModified).
func TestQuietStartupUnmodifiedValueSurvivesAnotherSave(t *testing.T) {
	sm := writeSettingsLayers(t, `{"quietStartup":"foo"}`, `{}`)
	settingsOK(t, sm.SetTuiMode("regular"))
	settingsOK(t, sm.Flush())
	assertSettingsFileJSON(t, sm.GlobalPath(), `{"quietStartup":"foo","tuiMode":"regular"}`)
}

// The JSON value and the /settings spelling are upstream's: false, true and "header" (settings-selector.ts:557-559, 923-925).
func TestQuietStartupValueSpelling(t *testing.T) {
	for _, tc := range []struct {
		value QuietStartup
		json  string
		text  string
	}{
		{QuietStartupFalse, `false`, "false"},
		{QuietStartupTrue, `true`, "true"},
		{QuietStartupHeader, `"header"`, "header"},
	} {
		data, err := json.Marshal(tc.value)
		settingsOK(t, err)
		settingsEqual(t, string(data), tc.json)
		settingsEqual(t, tc.value.String(), tc.text)
	}

	var item settingItem
	for _, candidate := range settingsItems() {
		if candidate.id == "quiet-startup" {
			item = candidate
		}
	}
	settingsEqual(t, item.values, []string{"true", "header", "false"})
	settingsEqual(t, item.desc, "Disable verbose printing at startup (header: keep only the startup header)")
	for _, value := range item.values {
		var s Settings
		item.apply(&s, value)
		settingsEqual(t, item.get(s), value)
		settingsEqual(t, s.quietStartupSet, true)
	}
}

// settings-selector.ts:705-711 (Pi 1.0.0): the TUI mode item describes the regular mode, now that fullscreen is the default, and reads an unset value as fullscreen.
func TestTuiModeSettingItemUpstream(t *testing.T) {
	var item settingItem
	for _, candidate := range settingsItems() {
		if candidate.id == "tui-mode" {
			item = candidate
		}
	}
	settingsEqual(t, item.label, "TUI mode")
	settingsEqual(t, item.desc, "Interface layout; regular mode uses the terminal's normal scrollback")
	settingsEqual(t, item.values, []string{"regular", "fullscreen"})
	settingsEqual(t, item.get(Settings{}), "fullscreen")
	settingsEqual(t, item.get(Settings{TuiMode: "regular"}), "regular")
}
