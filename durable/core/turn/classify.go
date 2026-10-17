// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"math"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// The response classifiers of pi-ai utils/retry.ts and utils/overflow.ts, over an assistant message object.

// The patterns are compiled when a failed assistant message is classified, not at package initialization: a session
// without an error never pays for them (they cost about 0.8 ms of the TinyGo core's initialization). The core keeps no package-level state
// it assigns after initialization (purity_test.go), so each classification compiles the patterns it reaches, in order, and
// stops at the first match. TestEveryClassifierPatternCompiles keeps every pattern compiled by a test.

// matchesAny reports whether text matches one of patterns, compiling each only when the ones before it did not match.
func matchesAny(patterns []string, text string) bool {
	for _, pattern := range patterns {
		if compileRx(pattern).matchRx(text) {
			return true
		}
	}
	return false
}

func stringField(o *jv.Object, key string) (string, bool) {
	v, _ := o.Get(key)
	s, ok := v.(string)
	return s, ok
}

// IsRetryableAssistantError is isRetryableAssistantError: a failed message whose error looks like a transient provider or
// transport error, and not like quota or billing exhaustion.
func IsRetryableAssistantError(message *jv.Object) bool {
	if stop, _ := stringField(message, "stopReason"); stop != "error" {
		return false
	}
	text, _ := stringField(message, "errorMessage")
	if text == "" {
		return false
	}
	if matchesAny(nonRetryablePatterns, text) {
		return false
	}
	return matchesAny(retryablePatterns, text)
}

// IsContextOverflow is isContextOverflow(message) without a context window: an error message that matches an overflow
// pattern and no non-overflow pattern, or the Cerebras bodyless 400/413.
func IsContextOverflow(message *jv.Object) bool {
	if stop, _ := stringField(message, "stopReason"); stop != "error" {
		return false
	}
	text, _ := stringField(message, "errorMessage")
	if text == "" {
		return false
	}
	if matchesAny(nonOverflowPatterns, text) {
		return false
	}
	if matchesAny(overflowPatterns, text) {
		return true
	}
	provider, _ := stringField(message, "provider")
	//portlint:allow providerliteral Pi special-cases Cerebras's bodyless overflow (packages/ai/src/utils/overflow.ts:146)
	return provider == "cerebras" && compileRx(cerebrasBodylessPattern).matchRx(text)
}

// RetryDelayMs is retryDelayMs: baseDelayMs * 2^(attempt-1), capped at maxAgentDelayMs (60 seconds when absent).
func RetryDelayMs(baseDelayMs float64, maxAgentDelayMs *float64, attempt float64) float64 {
	delay := baseDelayMs * math.Pow(2, math.Max(0, attempt-1))
	const maxSafe = 9007199254740991
	if math.IsNaN(delay) || math.Abs(delay) > maxSafe || delay != math.Trunc(delay) {
		delay = maxSafe
	}
	limit := 60000.0
	if maxAgentDelayMs != nil {
		limit = *maxAgentDelayMs
	}
	return math.Min(delay, limit)
}
