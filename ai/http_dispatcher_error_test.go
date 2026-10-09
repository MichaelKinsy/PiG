package ai

import "testing"

// http-dispatcher.ts:83-85: configureHttpDispatcher throws `Invalid HTTP idle timeout: <value>` for a negative timeout and keeps the configured one.
func TestConfigureHTTPDispatcherRejectsANegativeTimeoutWithPiMessage(t *testing.T) {
	previous := ConfiguredHTTPIdleTimeoutMs()
	err := ConfigureHTTPDispatcher(-5)
	if err == nil || err.Error() != "Invalid HTTP idle timeout: -5" {
		t.Fatalf("error = %v, want Invalid HTTP idle timeout: -5", err)
	}
	if got := ConfiguredHTTPIdleTimeoutMs(); got != previous {
		t.Fatalf("timeout after a rejected value = %d, want %d", got, previous)
	}
}
