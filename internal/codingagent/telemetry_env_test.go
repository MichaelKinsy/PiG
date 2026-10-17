package codingagent

import "testing"

// packages/coding-agent/src/core/telemetry.ts:3-13 (isTruthyEnvFlag, isInstallTelemetryEnabled): a set PI_TELEMETRY decides alone, and only
// "1", "true" and "yes" in any letter case enable it. An empty value is set and falsy, so it disables telemetry that the setting enables.
func TestInstallTelemetryEnvFlag(t *testing.T) {
	enabled := true
	manager := &SettingsManager{merged: Settings{EnableInstallTelemetry: &enabled}}
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"1", true}, {"true", true}, {"TRUE", true}, {"True", true}, {"yes", true}, {"YES", true}, {"Yes", true},
		{"", false}, {"0", false}, {"false", false}, {"no", false}, {"on", false}, {"y", false}, {"2", false}, {" 1", false}, {"1 ", false}, {"truee", false},
	} {
		t.Setenv("PI_TELEMETRY", tc.value)
		if got := manager.IsInstallTelemetryEnabled(); got != tc.want {
			t.Errorf("PI_TELEMETRY=%q with the setting enabled: got %v, want %v", tc.value, got, tc.want)
		}
	}
}
