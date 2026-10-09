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

	"github.com/MichaelKinsy/PiG/ai"
)

// completeSimpleWithRetries runs call through ai.RetryAssistantCall, upstream's
// retryAssistantCall: a cancelled ctx is never retried, non-retryable errors and
// budget exhaustion return the final error, and OnRetryFinished fires once iff a
// retry occurred. A nil or disabled policy means a single unretried call. The
// policy and callbacks are ai.RetryPolicy and ai.RetryCallbacks, the types Pi's
// compaction functions take as `retry` and `callbacks` (compaction.ts:624-625).
func completeSimpleWithRetries(ctx context.Context, policy *ai.RetryPolicy, callbacks ai.RetryCallbacks, call func() (string, *ai.Usage, error)) (string, *ai.Usage, error) {
	var (
		text          string
		usage         *ai.Usage
		callErr       error
		abortedInCall bool
	)
	response, err := ai.RetryAssistantCall(ctx, func() (ai.AssistantMessage, error) {
		text, usage, callErr = call()
		switch {
		case callErr == nil:
			return ai.AssistantMessage{StopReason: ai.StopReasonStop}, nil
		case ctx.Err() != nil:
			abortedInCall = true
			return ai.AssistantMessage{StopReason: ai.StopReasonAborted}, nil
		}
		return ai.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: callErr.Error()}, nil
	}, policy, callbacks)
	if err != nil {
		return "", nil, err
	}
	switch response.StopReason {
	case ai.StopReasonAborted:
		if abortedInCall {
			return "", nil, callErr
		}
		// The backoff sleep was cancelled.
		return "", nil, ctx.Err()
	case ai.StopReasonError:
		return "", nil, callErr
	}
	return text, usage, nil
}
