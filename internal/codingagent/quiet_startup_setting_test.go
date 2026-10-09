package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/settings-selector.ts

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
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

	item := settingsSelectorRow(t, SettingsConfig{}, "quiet-startup")
	settingsEqual(t, item.Values, []string{"true", "header", "false"})
	settingsEqual(t, item.Description, "Disable verbose printing at startup (header: keep only the startup header)")
	for _, value := range item.Values {
		// The row shows the saved value, and cycling to it reports the QuietStartup that spells it.
		var shown QuietStartup
		switch value {
		case "true":
			shown = QuietStartupTrue
		case "header":
			shown = QuietStartupHeader
		}
		settingsEqual(t, settingsSelectorRow(t, SettingsConfig{QuietStartup: shown}, "quiet-startup").CurrentValue, value)
		sm := NewSettingsManager(t.TempDir(), t.TempDir())
		var reported []QuietStartup
		list := NewSettingsSelectorComponent(SettingsConfig{QuietStartup: shown}, SettingsCallbacks{OnQuietStartupChange: func(quiet QuietStartup) {
			reported = append(reported, quiet)
			settingsOK(t, sm.SetQuietStartup(quiet))
		}}).GetSettingsList()
		list.SelectItem("quiet-startup")
		list.HandleInput("\r")
		next := item.Values[(slices.Index(item.Values, value)+1)%len(item.Values)]
		settingsEqual(t, len(reported), 1)
		settingsEqual(t, reported[0].String(), next)
		settingsEqual(t, sm.Get().quietStartupSet, true)
		settingsEqual(t, sm.GetQuietStartup().String(), next)
	}
}

// settings-selector.ts:705-711 (Pi 1.0.0): the TUI mode item describes the regular mode, now that fullscreen is the default, and reads an unset value as fullscreen.
func TestTuiModeSettingItemUpstream(t *testing.T) {
	item := settingsSelectorRow(t, SettingsConfig{TuiMode: "fullscreen"}, "tui-mode")
	settingsEqual(t, item.Label, "TUI mode")
	settingsEqual(t, item.Description, "Interface layout; regular mode uses the terminal's normal scrollback")
	settingsEqual(t, item.Values, []string{"regular", "fullscreen"})
	settingsEqual(t, item.CurrentValue, "fullscreen")
	settingsEqual(t, settingsSelectorRow(t, SettingsConfig{TuiMode: "regular"}, "tui-mode").CurrentValue, "regular")
	// An unset saved mode reads as fullscreen when the config is built from the settings.
	sc := &SlashContext{SettingsManager: NewSettingsManager(t.TempDir(), t.TempDir())}
	config, err := settingsConfig(sc)
	settingsOK(t, err)
	settingsEqual(t, config.TuiMode, "fullscreen")
}

// settingsSelectorRow returns the selector's row with the given id.
func settingsSelectorRow(t *testing.T, config SettingsConfig, id string) tui.SettingItem {
	t.Helper()
	for _, item := range NewSettingsSelectorComponent(config, SettingsCallbacks{}).GetSettingsList().Items() {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("the settings selector has no %q row", id)
	return tui.SettingItem{}
}
