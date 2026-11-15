package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// With no setter called, a session's retry settings are upstream's defaults: 3 retries (settings-manager.ts:21), 2000 ms base (:22) and the 60000 ms cap, as literals here so a changed built-in cannot pass by comparing with itself.
// mutation-checked: builtinRetryConfig with MaxRetries 4, or DefaultRetryConfig returning a zero RetryConfig, fails.
func TestRetrySettingsWithoutASetterAreUpstreamsDefaults(t *testing.T) {
	t.Cleanup(func() { defaultRetryConfig.Store(nil) })
	defaultRetryConfig.Store(nil)
	want := RetryConfig{Enabled: true, MaxRetries: 3, BaseDelayMs: 2000, MaxAgentDelayMs: 60000}
	if got := DefaultRetryConfig(); got != want {
		t.Fatalf("DefaultRetryConfig() = %+v, want %+v", got, want)
	}
	if got := NewInMemorySettingsManager(Settings{}).GetRetrySettings(); got != want {
		t.Fatalf("GetRetrySettings() with no setter and no settings = %+v, want %+v", got, want)
	}
}

func TestDefaultRetryConfigIsReplaceable(t *testing.T) {
	t.Cleanup(func() {
		defaultRetryConfig.Store(nil)
		SetDefaultRetryDecider(nil, false)
		SetDefaultHTTPIdleTimeoutMs(nil)
	})
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

	idle := 10_000
	SetDefaultHTTPIdleTimeoutMs(&idle)
	if got, err := sm.GetHttpIdleTimeoutMs(); err != nil || got != 10_000 {
		t.Fatalf("default HTTPIdleTimeoutMs = %d, %v", got, err)
	}
	explicitIdle := 25_000
	smIdle := NewInMemorySettingsManager(Settings{HTTPIdleTimeoutMs: &explicitIdle})
	if got, err := smIdle.GetHttpIdleTimeoutMs(); err != nil || got != 25_000 {
		t.Fatalf("settings.json HTTPIdleTimeoutMs = %d, %v", got, err)
	}

	SetDefaultRetryDecider(func(int, int, ai.AssistantMessage) ai.RetryDecision {
		return ai.RetryDecision{Retry: true, DelayMs: 250}
	}, true)
	if DefaultRetryDecider() == nil || !DefaultRetrySilent() {
		t.Fatal("DefaultRetryDecider / DefaultRetrySilent not installed")
	}
	m := statusBorderMode(t, true)
	m.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 5, DelayMs: 1000})
	if m.activeStatusIndicator != nil {
		t.Fatal("silent retry policy showed status indicator")
	}
	m.handleAgentEvent(agent.AutoRetryEndEvent{Success: false, Attempt: 5, FinalError: "boom"})
	if chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(80), "\n")); strings.Contains(chat, "Retry failed") {
		t.Fatalf("silent retry policy rendered Retry failed error: %q", chat)
	}
}
