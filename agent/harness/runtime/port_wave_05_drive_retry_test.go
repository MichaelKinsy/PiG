package runtime

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

func TestPortWave05RuntimeRetryDelay(t *testing.T) {
	t.Parallel()
	// upstream: packages/agent/test/harness/runtime/drive-retry.test.ts:5
	t.Run("uses capped delay when computing retry readiness", func(t *testing.T) {
		t.Parallel()
		policy := session.NormalizedRetryPolicy{BaseDelayMs: 2000, MaxAgentDelayMs: 30000}
		if got := RetryNotBefore(policy, 5, 100); got != 30100 {
			t.Fatalf("retry readiness = %d, want 30100", got)
		}
	})
}
