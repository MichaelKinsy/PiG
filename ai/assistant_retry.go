package ai

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
)

func buildProviderErrorPattern(patterns []string) *regexp.Regexp {
	return regexp.MustCompile("(?i)" + strings.Join(patterns, "|"))
}

var nonRetryableProviderLimitErrorPattern = buildProviderErrorPattern([]string{
	// OpenCode Go/free-tier limits returned as 429 JSON error types by
	// OpenCode's Zen API are subscription/account limits.
	"GoUsageLimitError",
	"FreeUsageLimitError",
	"Monthly usage limit reached",
	"available balance",
	// Generic quota/budget/billing exhaustion.
	"insufficient_quota",
	"out of budget",
	"quota exceeded",
	"billing",
})

var retryableProviderErrorPattern = buildProviderErrorPattern([]string{
	// Generic provider load, HTTP status, and server-side transient failures.
	"overloaded",
	"currently experiencing high demand",
	"rate.?limit",
	"too many requests",
	"429",
	"500",
	"502",
	"503",
	"504",
	"520",
	"524",
	"service.?unavailable",
	"server.?error",
	"internal.?error",
	// Wrapper/provider text for transient upstream failures.
	"provider.?returned.?error",
	"exceeded request buffer limit while retrying upstream",
	// Network, proxy, and fetch transport failures.
	"network.?error",
	"connection.?error",
	"connection.?refused",
	"connection.?lost",
	"other side closed",
	"fetch failed",
	"getaddrinfo",
	"ENOTFOUND",
	"EAI_AGAIN",
	"upstream.?connect",
	"reset before headers",
	"socket hang up",
	"socket connection was closed",
	"timed? out",
	"timeout",
	"terminated",
	// WebSocket transports can report close/error text.
	"websocket.?closed",
	"websocket.?error",
	// Premature stream endings from SDKs and transports.
	"ended without",
	"stream ended before message_stop",
	"stream ended before a terminal response event",
	"http2 request did not get a response",
	// Provider-requested retry delay cap failures flow through the outer policy.
	"retry delay",
	// Explicit retry guidance emitted mid-stream.
	"you can retry your request",
	"try your request again",
	"please retry your request",
	// gRPC based providers.
	"ResourceExhausted",
})

// RetryPolicy bounds attempts with exponential backoff
// (baseDelayMs * 2^(attempt-1)). MaxAgentDelayMs caps each computed delay and
// defaults to DefaultMaxAgentRetryDelayMs when nil.
type RetryPolicy struct {
	Enabled bool `json:"enabled"`
	// MaxRetries is the retry budget; the initial call never counts as a retry.
	MaxRetries      int  `json:"maxRetries"`
	BaseDelayMs     int  `json:"baseDelayMs"`
	MaxAgentDelayMs *int `json:"maxAgentDelayMs,omitempty"`
}

// DefaultMaxAgentRetryDelayMs is the default cap for one agent retry delay.
const DefaultMaxAgentRetryDelayMs = 60_000

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

// RetryDelayMs returns the capped exponential delay for a 1-based attempt.
// Overflowing products saturate at Number.MAX_SAFE_INTEGER before the cap.
func RetryDelayMs(baseDelayMs int, maxAgentDelayMs *int, attempt int) int {
	delay := float64(baseDelayMs) * math.Pow(2, max(0, float64(attempt)-1))
	safeDelay := maxSafeInteger
	if math.Abs(delay) <= maxSafeInteger && delay == math.Trunc(delay) {
		safeDelay = int(delay)
	}
	limit := DefaultMaxAgentRetryDelayMs
	if maxAgentDelayMs != nil {
		limit = *maxAgentDelayMs
	}
	return min(safeDelay, limit)
}

// RetryCallbacks are emitted by RetryAssistantCall around each retry. A nil
// field is skipped; a returned error fails the retry call.
type RetryCallbacks struct {
	// OnRetryScheduled runs before the backoff sleep of each 1-indexed retry.
	OnRetryScheduled func(attempt, maxAttempts, delayMs int, errorMessage string) error
	// OnRetryAttemptStart runs after the backoff sleep, before the retried call.
	OnRetryAttemptStart func() error
	// OnRetryFinished runs once when a retried loop ends. finalError is empty
	// when upstream passes no final error.
	OnRetryFinished func(success bool, attempt int, finalError string) error
}

// RetryAssistantCall runs one assistant-producing call with bounded retry on
// transient errors. Successful and aborted responses return immediately; a
// non-retryable error or an exhausted budget returns the final error message.
// Cancellation during a backoff sleep returns the failed response normalized
// to stopReason "aborted" without its error message. A nil or disabled policy
// returns the first response unchanged.
func RetryAssistantCall(ctx context.Context, produce func() (AssistantMessage, error), policy *RetryPolicy, callbacks RetryCallbacks) (AssistantMessage, error) {
	maxAttempts := 0
	if policy != nil && policy.Enabled {
		maxAttempts = policy.MaxRetries
	}
	attempt := 0
	lastRetryAttempt := 0
	lastRetryMessage := ""
	for {
		response, err := produce()
		if err != nil {
			return AssistantMessage{}, err
		}
		if response.StopReason == StopReasonAborted {
			if lastRetryAttempt > 0 {
				if err := callFinished(callbacks, false, lastRetryAttempt, ""); err != nil {
					return AssistantMessage{}, err
				}
			}
			return response, nil
		}
		if response.StopReason != StopReasonError {
			if lastRetryAttempt > 0 {
				if err := callFinished(callbacks, true, lastRetryAttempt, ""); err != nil {
					return AssistantMessage{}, err
				}
			}
			return response, nil
		}
		if attempt >= maxAttempts || !IsRetryableAssistantError(response) {
			if lastRetryAttempt > 0 {
				if err := callFinished(callbacks, false, lastRetryAttempt, response.ErrorMessage); err != nil {
					return AssistantMessage{}, err
				}
			}
			return response, nil
		}

		attempt++
		lastRetryAttempt = attempt
		lastRetryMessage = response.ErrorMessage
		if lastRetryMessage == "" {
			lastRetryMessage = "Unknown error"
		}
		delayMs := RetryDelayMs(policy.BaseDelayMs, policy.MaxAgentDelayMs, attempt)
		if callbacks.OnRetryScheduled != nil {
			if err := callbacks.OnRetryScheduled(attempt, maxAttempts, delayMs, lastRetryMessage); err != nil {
				return AssistantMessage{}, err
			}
		}
		if err := sleepContext(ctx, retryTimerDuration(delayMs)); err != nil {
			if finishErr := callFinished(callbacks, false, attempt, lastRetryMessage); finishErr != nil {
				return AssistantMessage{}, finishErr
			}
			response.ErrorMessage = ""
			response.StopReason = StopReasonAborted
			return response, nil
		}
		if callbacks.OnRetryAttemptStart != nil {
			if err := callbacks.OnRetryAttemptStart(); err != nil {
				return AssistantMessage{}, err
			}
		}
	}
}

func callFinished(callbacks RetryCallbacks, success bool, attempt int, finalError string) error {
	if callbacks.OnRetryFinished == nil {
		return nil
	}
	return callbacks.OnRetryFinished(success, attempt, finalError)
}

var errRetrySleepAborted = errors.New("Aborted")

// Node setTimeout uses one millisecond for delays outside its signed 32-bit
// millisecond range. Normalize before converting to nanoseconds to avoid overflow.
func retryTimerDuration(delayMs int) time.Duration {
	if delayMs < 1 || delayMs > 1<<31-1 {
		return time.Millisecond
	}
	return time.Duration(delayMs) * time.Millisecond
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if ctx.Err() != nil {
		return errRetrySleepAborted
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return errRetrySleepAborted
	}
}

// IsRetryableAssistantError classifies whether a failed assistant message looks
// like a transient provider or transport error. It does not implement retry
// policy; callers handle context overflow first.
func IsRetryableAssistantError(message AssistantMessage) bool {
	if message.StopReason != StopReasonError || message.ErrorMessage == "" {
		return false
	}
	if nonRetryableProviderLimitErrorPattern.MatchString(message.ErrorMessage) {
		return false
	}
	return retryableProviderErrorPattern.MatchString(message.ErrorMessage)
}
