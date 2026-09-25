// Retry policy for summarization LLM calls.
//
// Mirrors upstream:
//
//	.upstream/current/packages/ai/src/utils/retry.ts (RetryPolicy, RetryCallbacks, retryAssistantCall)
//	.upstream/current/packages/agent/src/harness/compaction/compaction.ts (completeSimpleWithRetries)
//
// Compaction and branch-summary summarization calls reuse settings.retry so a
// single transient stream drop no longer fails the whole operation.
package compaction

import (
	"context"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// RetryPolicy is the bounded-attempts, exponential-backoff policy for
// summarization retries. Mirrors upstream RetryPolicy.
type RetryPolicy struct {
	Enabled bool
	// MaxRetries is the max retry attempts (0 = no retries). The initial call
	// never counts as a retry.
	MaxRetries int
	// BaseDelayMs is the base backoff; per-attempt delay is
	// BaseDelayMs * 2^(attempt-1).
	BaseDelayMs int
	// MaxAgentDelayMs caps each delay; nil uses ai.DefaultMaxAgentRetryDelayMs.
	MaxAgentDelayMs *int
}

// RetryCallbacks are emitted around each retry. Nil fields are skipped.
// Mirrors upstream RetryCallbacks.
type RetryCallbacks struct {
	// OnRetryScheduled fires before the backoff sleep of each retry (1-indexed).
	OnRetryScheduled func(attempt, maxAttempts, delayMs int, errMsg string)
	// OnRetryAttemptStart fires after the backoff sleep, before the retried call.
	OnRetryAttemptStart func()
	// OnRetryFinished fires once when a retried loop ends.
	OnRetryFinished func()
}

// RetryOptions bundles the policy, transient-error classifier, and callbacks.
// A nil *RetryOptions disables retries. IsRetryable is injected so this package
// need not import the coding-layer classifier (which would be an import cycle).
type RetryOptions struct {
	Policy      RetryPolicy
	IsRetryable func(errMsg string) bool
	Callbacks   RetryCallbacks
}

// completeSimpleWithRetries runs call with bounded exponential-backoff retries
// on transient errors. Mirrors upstream completeSimpleWithRetries +
// retryAssistantCall: a cancelled ctx is never retried, non-retryable errors and
// budget exhaustion return the final error, and OnRetryFinished fires once iff a
// retry occurred. opts == nil (or disabled) means a single unretried call.
func completeSimpleWithRetries(ctx context.Context, opts *RetryOptions, call func() (string, *ai.Usage, error)) (string, *ai.Usage, error) {
	maxAttempts := 0
	if opts != nil && opts.Policy.Enabled {
		maxAttempts = opts.Policy.MaxRetries
	}

	attempt := 0
	retried := false
	finish := func() {
		if retried && opts != nil && opts.Callbacks.OnRetryFinished != nil {
			opts.Callbacks.OnRetryFinished()
		}
	}

	for {
		out, usage, err := call()
		if err == nil {
			finish()
			return out, usage, nil
		}
		// Abort: terminal but not a retry candidate. Never retry a cancelled call.
		if ctx.Err() != nil {
			finish()
			return "", nil, err
		}
		retryable := opts != nil && opts.IsRetryable != nil && opts.IsRetryable(err.Error())
		if attempt >= maxAttempts || !retryable {
			finish()
			return "", nil, err
		}

		attempt++
		retried = true
		delayMs := ai.RetryDelayMs(opts.Policy.BaseDelayMs, opts.Policy.MaxAgentDelayMs, attempt)
		if opts.Callbacks.OnRetryScheduled != nil {
			opts.Callbacks.OnRetryScheduled(attempt, maxAttempts, delayMs, err.Error())
		}
		select {
		case <-ctx.Done():
			finish()
			return "", nil, ctx.Err()
		case <-time.After(time.Duration(delayMs) * time.Millisecond):
		}
		if opts.Callbacks.OnRetryAttemptStart != nil {
			opts.Callbacks.OnRetryAttemptStart()
		}
	}
}
