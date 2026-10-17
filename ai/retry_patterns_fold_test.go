package ai

import "testing"

// packages/ai/src/utils/retry.ts:3-4 builds both provider error patterns with new RegExp(..., "i"), and openai-codex-responses.ts:136 tests
// /rate.?limit|overloaded|service.?unavailable|upstream.?connect|connection.?refused/i. Non-unicode JavaScript: "." stops at \r, U+2028 and
// U+2029, and /i folds neither U+017F onto s nor U+212A onto k (Node 24: /rate.?limit/i.test("rate\rlimit") and
// /too many requests/i.test("too many requeſts") are false). Go's "." and (?i) match all of these.
func TestProviderRetryPatternsMatchLikeJavaScript(t *testing.T) {
	retryable := func(text string) bool {
		return IsRetryableAssistantError(AssistantMessage{StopReason: StopReasonError, ErrorMessage: text})
	}
	for text, want := range map[string]bool{
		"rate-limit":        true,
		"RATE LIMIT":        true,
		"rate\rlimit":       false,
		"rate\u2028limit":   false,
		"too many requests": true,
		"server_busy":       true,
		"The servers are currently busy, try later": true,
		"too many requeſts":                         false,
		"Service Unavailable":                       true,
		"ſervice unavailable":                       false,
		"quota exceeded: overloaded":                false,
		"quota exceeded":                            false,
		"quota exceeded\u2029 later":                false,
		"Out of budget":                             false,
		"out of budget, rate limit":                 false,
		"out of budget? no; overload":               false,
	} {
		if got := retryable(text); got != want {
			t.Errorf("IsRetryableAssistantError(%q) = %v, want %v", text, got, want)
		}
	}
	// The non-retryable limit pattern folds ASCII only too: with a long s the subscription limit is not recognized and the retryable 429
	// wins (Node 24: true for "Monthly uſage limit reached 429", false for "Monthly usage limit reached 429").
	if !retryable("Monthly uſage limit reached 429") || retryable("MONTHLY USAGE LIMIT REACHED 429") {
		t.Error("the non-retryable limit pattern must fold ASCII letters only")
	}
	for text, want := range map[string]bool{
		"Rate limit":              true,
		"rate\rlimit":             false,
		"Connection Refused":      true,
		"connection\u2028refused": false,
		"upſtream connect":        false,
	} {
		if got := codexRetryableMessage.MatchString(text); got != want {
			t.Errorf("codexRetryableMessage(%q) = %v, want %v", text, got, want)
		}
	}
	if codexTerminalRateLimit.MatchString("quota exceedeſ") == false && codexTerminalRateLimit.MatchString("Quota Exceeded") {
		return
	}
	t.Error("codexTerminalRateLimit must fold ASCII letters only")
}

// packages/ai/src/utils/retry.ts:32-34 (Pi 1.1.0, #10543): server_busy and "servers are currently busy" provider errors are retried instead of ending the turn.
func TestServerBusyProviderErrorsAreRetryable(t *testing.T) {
	for text, want := range map[string]bool{
		`{"error":{"type":"server_busy","message":"try later"}}`: true,
		"The servers are currently busy, please try again":       true,
		"SERVER_BUSY":                         true,
		"Servers Are Currently Busy":          true,
		"server is busy":                      false,
		"quota exceeded: server_busy":         false,
		"servers are currently busy, billing": false,
	} {
		got := IsRetryableAssistantError(AssistantMessage{StopReason: StopReasonError, ErrorMessage: text})
		if got != want {
			t.Errorf("IsRetryableAssistantError(%q) = %v, want %v", text, got, want)
		}
	}
}
