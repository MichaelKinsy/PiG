package coding

import "testing"

// agent-session.ts:371,477: settingsManager is the SettingsManager the Session was configured with, and the same
// instance every later read of Session policy goes through.
func TestSessionSettingsManagerIsTheServicesSettings(t *testing.T) {
	session := autoQueueSession(t, false)
	if session.SettingsManager() == nil {
		t.Fatal("SettingsManager() is nil")
	}
	if session.SettingsManager() != session.Services().SettingsManager() {
		t.Fatal("SettingsManager() is not the services' instance")
	}
}
