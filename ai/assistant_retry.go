package ai

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// buildProviderErrorPattern is retry.ts buildProviderErrorPattern: new RegExp(patterns.join("|"), "i").
func buildProviderErrorPattern(patterns []string) *lazyregexp.Regexp {
	return lazyregexp.NewJSIgnoreCase(strings.Join(patterns, "|"))
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
	// Sign in with ChatGPT: the subscription's shared usage limit, which resets after hours rather than seconds (utils/retry.ts).
	"subscription_sharing_usage_limit_exceeded",
	// Sign in with ChatGPT on a ChatGPT Business plan: a usage limit set by the workspace admin. Only the admin can raise it (utils/retry.ts, Pi cedefc5326).
	"subscription_sharing_workplace_individual_usage_limit_exceeded",
})

var retryableProviderErrorPattern = buildProviderErrorPattern([]string{
	// Generic provider load, HTTP status, and server-side transient failures.
	"overloaded",
	// #10543: a provider that answers server_busy or "servers are currently busy" is transient (utils/retry.ts).
	"server_busy",
	"servers are currently busy",
	"currently experiencing high demand",
	"model is at capacity",
	"rate.?limit",
	"too many requests",
	// Status codes must not be part of a longer word or number, e.g. a request id or byte count (#11057, Pi 6e491cfa).
	// Pi spells it (?<![a-z0-9])(?:429|500|502|503|504|520|524)(?![a-z0-9]); RE2 has no lookaround, so the neighbours are matched, which a boolean test cannot tell apart.
	"(?:^|[^a-z0-9])(?:429|500|502|503|504|520|524)(?:[^a-z0-9]|$)",
	// Also matches "The service is temporarily unavailable" (#10965, utils/retry.ts).
	"service.?(?:is.?)?(?:temporarily.?)?unavailable",
	"server.?error",
	"internal.?error",
	// Wrapper/provider text for transient upstream failures: "Provider returned error" (#2264) and gateway wording such as
	// "The upstream provider returned an error" (#10842, utils/retry.ts).
	"provider.?returned.?(?:an.?)?error",
	"exceeded request buffer limit while retrying upstream",
	// Gateway failure relaying the upstream response stream, sent as an OpenAI Responses response.failed event without an error code (#10920, Pi 7efebff1).
	"unable to forward upstream",
	// Other gateway upstream failures relayed as response.failed error codes (#10920, Pi 698a2d23).
	"upstream.?request.?failed",
	"upstream_stream_read_error",
	"stream was interrupted",
	"http/?2 stream failed",
	// Network, proxy, and fetch transport failures.
	"network.?error",
	"connection.?error",
	"connection.?refused",
	"connection.?lost",
	"other side closed",
	"fetch failed",
	// node-fetch system errors, e.g. the Google ADC OAuth token refresh used by google-vertex; the reason is often empty (#10996, Pi 0fbdbd3f).
	"request to \\S+ failed, reason:",
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
	// Codex CLI mid-stream disconnect, relayed by proxies that wrap the Codex CLI, e.g. "stream disconnected before completion: stream closed before response.completed" (#11057, Pi 29057ece).
	"stream disconnected before completion",
	// Node ERR_HTTP2_STREAM_CANCEL: the HTTP/2 session died before the request was sent, for example after the Bedrock SDK's 5-minute session timeout (utils/retry.ts, #10379).
	"pending stream has been canceled",
	// Provider-requested retry delay cap failures flow through the outer policy.
	"retry delay",
	// Explicit retry guidance emitted mid-stream.
	"you can retry your request",
	"try your request again",
	"please retry your request",
	// gRPC based providers.
	"ResourceExhausted",
	// Sign in with ChatGPT: usage or user data temporarily unavailable. Usage failures can arrive mid-stream without an HTTP 503 in the message (utils/retry.ts).
	"subscription_sharing_usage_unavailable",
	"subscription_sharing_user_unavailable",
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

// RetryDelayMs returns the capped exponential delay for a 1-based attempt from the policy base delay and agent cap (only BaseDelayMs and MaxAgentDelayMs are read).
// Overflowing products saturate at Number.MAX_SAFE_INTEGER before the cap.
func RetryDelayMs(policy RetryPolicy, attempt int) int {
	delay := float64(policy.BaseDelayMs) * math.Pow(2, max(0, float64(attempt)-1))
	safeDelay := maxSafeInteger
	if math.Abs(delay) <= maxSafeInteger && delay == math.Trunc(delay) {
		safeDelay = int(delay)
	}
	limit := DefaultMaxAgentRetryDelayMs
	if policy.MaxAgentDelayMs != nil {
		limit = *policy.MaxAgentDelayMs
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
		delayMs := RetryDelayMs(*policy, attempt)
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
