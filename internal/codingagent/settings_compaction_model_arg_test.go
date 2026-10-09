package codingagent

import "testing"

// settings-manager.ts getCompactionSettings(model?) resolves each token through the model override, then the ordinary setting, then the default; without a model only the ordinary settings apply.
// Pi: packages/coding-agent/src/core/settings-manager.ts:964 (SettingsManager.getCompactionSettings).
func TestGetCompactionSettingsTakesAnOptionalModel(t *testing.T) {
	sm := compactionSettingsMemory(t, `{"compaction":{"reserveTokens":8192,"keepRecentTokens":10000,"modelOverrides":{"provider/family/model":{"reserveTokens":400000}}}}`)
	plain, err := sm.GetCompactionSettings()
	if err != nil || plain.ReserveTokens != 8192 || plain.KeepRecentTokens != 10000 || !plain.Enabled {
		t.Fatalf("without a model: %+v, %v", plain, err)
	}
	overridden, err := sm.GetCompactionSettings(compactionTestModel("provider", "family/model"))
	if err != nil || overridden.ReserveTokens != 400000 || overridden.KeepRecentTokens != 10000 {
		t.Fatalf("with the overridden model: %+v, %v", overridden, err)
	}
	other, err := sm.GetCompactionSettings(compactionTestModel("provider", "other"))
	if err != nil || other != plain {
		t.Fatalf("with another model: %+v, %v; want %+v", other, err, plain)
	}
}
