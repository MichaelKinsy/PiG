package coding

import "testing"

// Upstream agent-session.ts setAutoRetryEnabled(enabled) is settingsManager.setRetryEnabled(enabled): it writes global
// retry.enabled, which getRetryEnabled (`retry?.enabled ?? true`) then reports.
// Pi: packages/coding-agent/src/core/settings-manager.ts:987 (SettingsManager.getRetryEnabled).
func TestSessionSetAutoRetryEnabledWritesTheRetrySetting(t *testing.T) {
	services := newTestServices(t)
	sess, err := NewSession(services, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	settings := services.SettingsManager()
	if !settings.GetRetryEnabled() {
		t.Fatal("retry is not enabled by default")
	}
	for _, enabled := range []bool{false, true} {
		if err := sess.SetAutoRetryEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		if got := settings.GetRetryEnabled(); got != enabled {
			t.Errorf("after SetAutoRetryEnabled(%v) GetRetryEnabled = %v", enabled, got)
		}
		if got := settings.GetRetrySettings().Enabled; got != enabled {
			t.Errorf("after SetAutoRetryEnabled(%v) retry settings enabled = %v", enabled, got)
		}
	}
}
