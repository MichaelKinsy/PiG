package runtime

import (
	"context"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/agent/src/harness/runtime/drive/retry.ts

var runtimeNow = func() int64 { return time.Now().UnixMilli() }
var runtimeNewTimer = time.NewTimer

// RetryNotBefore adds the capped retry delay to now, saturating unsafe JavaScript integer deadlines.
func RetryNotBefore(policy session.NormalizedRetryPolicy, attempt int, now int64) int64 {
	delay := int64(ai.RetryDelayMs(policy.BaseDelayMs, &policy.MaxAgentDelayMs, attempt))
	const maxSafeInteger int64 = 1<<53 - 1
	if now > maxSafeInteger-delay || now < -maxSafeInteger-delay {
		return maxSafeInteger
	}
	return now + delay
}

// WaitUntil waits until the Unix-millisecond deadline, releasing its timer on cancellation and returning the signal's cause.
func WaitUntil(ctx context.Context, notBefore int64) error {
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		remaining := notBefore - runtimeNow()
		if remaining <= 0 {
			return nil
		}
		// upstream: packages/agent/src/harness/runtime/drive/retry.ts:waitUntil
		timer := runtimeNewTimer(time.Duration(min(remaining, 2_147_483_647)) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return context.Cause(ctx)
		case <-timer.C:
		}
	}
}
