package harness

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/MichaelKinsy/PiG/ai"
)

// DefaultRetryPolicy is the harness default provider retry policy.
var DefaultRetryPolicy = ai.RetryPolicy{
	Enabled:         true,
	MaxRetries:      3,
	BaseDelayMs:     1_000,
	MaxAgentDelayMs: new(ai.DefaultMaxAgentRetryDelayMs),
}

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

func isSafeNonNegative(value int) bool {
	return value >= 0 && value <= maxSafeInteger
}

// ValidateToolNames rejects duplicate tool names.
func ValidateToolNames(tools []AgentHarnessTool) error {
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if names[tool.Name] {
			return fmt.Errorf("Duplicate tool name: %s", strconv.Quote(tool.Name))
		}
		names[tool.Name] = true
	}
	return nil
}

// ValidateRetryPolicy rejects retry counts and delays that are not finite
// non-negative safe integers, and a retry count whose attempt total would not
// remain safe.
func ValidateRetryPolicy(policy ai.RetryPolicy) error {
	if !isSafeNonNegative(policy.MaxRetries) || policy.MaxRetries == maxSafeInteger ||
		!isSafeNonNegative(policy.BaseDelayMs) ||
		(policy.MaxAgentDelayMs != nil && !isSafeNonNegative(*policy.MaxAgentDelayMs)) {
		return errors.New("Retry policy values must be finite non-negative safe integers")
	}
	return nil
}

// ValidateCompactionSettings rejects token counts that are not finite
// non-negative safe integers.
func ValidateCompactionSettings(settings CompactionSettings) error {
	if !isSafeNonNegative(settings.ReserveTokens) || !isSafeNonNegative(settings.KeepRecentTokens) {
		return errors.New("Compaction token counts must be finite non-negative safe integers")
	}
	return nil
}
