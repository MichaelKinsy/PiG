package codingagent

import "testing"

func TestDefaultRetryConfigIsReplaceable(t *testing.T) {
	t.Cleanup(func() { defaultRetryConfig.Store(nil) })
	sm := NewInMemorySettingsManager(Settings{})
	if got := sm.GetRetrySettings(); got != builtinRetryConfig {
		t.Fatalf("built-in defaults = %+v", got)
	}
	SetDefaultRetryConfig(RetryConfig{Enabled: true, MaxRetries: 5, BaseDelayMs: 500, MaxAgentDelayMs: 8000})
	if got := sm.GetRetrySettings(); got.MaxRetries != 5 || got.BaseDelayMs != 500 || got.MaxAgentDelayMs != 8000 {
		t.Fatalf("replaced defaults = %+v", got)
	}
	maxRetries := 1
	sm2 := NewInMemorySettingsManager(Settings{Retry: &RetrySettingsJSON{MaxRetries: &maxRetries}})
	if got := sm2.GetRetrySettings(); got.MaxRetries != 1 || got.BaseDelayMs != 500 {
		t.Fatalf("settings.json must override one field only, got %+v", got)
	}
}
