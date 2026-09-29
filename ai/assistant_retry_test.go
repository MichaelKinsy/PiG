package ai

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func retryFaux(text string, stopReason StopReason, errorMessage string) AssistantMessage {
	message := AssistantMessage{Content: []AssistantContentBlock{}, StopReason: stopReason, ErrorMessage: errorMessage}
	if text != "" {
		message.Content = append(message.Content, TextContent{Text: text})
	}
	if stopReason == "" {
		message.StopReason = StopReasonStop
	}
	return message
}

func errorFaux(errorMessage string) AssistantMessage {
	return retryFaux("", StopReasonError, errorMessage)
}

func TestIsRetryableAssistantErrorClassification(t *testing.T) {
	retryable := []struct{ name, message string }{
		// .upstream/v0.87.1/packages/ai/test/retry.test.ts:19
		{"matches explicit provider retry guidance/OpenAI", "An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID req_******** in your message."},
		{"matches explicit provider retry guidance/Bedrock", `{"message":"The system encountered an unexpected error during processing. Try your request again."}`},
		{"matches explicit provider retry guidance/NVIDIA", "ResourceExhausted: Worker local total request limit reached (288/48)"},
		// .upstream/v0.87.1/packages/ai/test/retry.test.ts:37
		{"matches Bun fetch socket drop wording", "The socket connection was closed unexpectedly. For more information, pass `verbose: true` in the second argument to fetch()"},
		// .upstream/v0.87.1/packages/ai/test/retry.test.ts:45
		{"matches upstream request buffer exhaustion wording", "Error: exceeded request buffer limit while retrying upstream"},
		// .upstream/v0.87.1/packages/ai/test/retry.test.ts:56 (all four table rows)
		{"matches DNS transport failure wording/wrapped", "The pending stream has been canceled (caused by: getaddrinfo ENOTFOUND bedrock-runtime.us-east-1.amazonaws.com)"},
		{"matches DNS transport failure wording/ENOTFOUND", "connect ENOTFOUND api.example.com"},
		{"matches DNS transport failure wording/EAI_AGAIN", "EAI_AGAIN api.example.com"},
		{"matches DNS transport failure wording/getaddrinfo", "getaddrinfo failed for api.example.com"},
		// .upstream/v0.87.1/packages/ai/test/retry.test.ts:65
		{"matches OpenAI Responses streams that end before terminal events", "OpenAI Responses stream ended before a terminal response event"},
		// .upstream/v0.87.1/packages/ai/test/retry.test.ts:73
		{"matches Azure peak-load capacity errors", "The system is currently experiencing high demand and cannot process your request. Your request exceeds the maximum usage size allowed during peak load. For improved capacity reliability, consider switching to Provisioned Throughput."},
		// .upstream/v0.87.1/packages/ai/test/retry.test.ts:88
		{"classifies assistant error messages/overloaded", "overloaded_error"},
		{"classifies assistant error messages/520", "520 status code (no body)"},
		{"classifies assistant error messages/524", "524 status code (no body)"},
	}
	for _, tc := range retryable {
		t.Run(tc.name, func(t *testing.T) {
			if !IsRetryableAssistantError(errorFaux(tc.message)) {
				t.Errorf("expected retryable: %q", tc.message)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/retry.test.ts:80
	if IsRetryableAssistantError(errorFaux("429 quota exceeded")) {
		t.Error("provider limit error must stay non-retryable")
	}
	if IsRetryableAssistantError(retryFaux("not an error", "", "")) {
		t.Error("successful message classified retryable")
	}
}

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:108 — caps agent retry delay.
func TestRetryDelayMsCapsAgentRetryDelay(t *testing.T) {
	if got := RetryDelayMs(2000, nil, 6); got != 60000 {
		t.Fatalf("default cap = %d", got)
	}
	if got := RetryDelayMs(2000, new(5000), 5); got != 5000 {
		t.Fatalf("explicit cap = %d", got)
	}
	if got := RetryDelayMs(2000, new(0), 5); got != 0 {
		t.Fatalf("zero cap = %d", got)
	}
	if got := RetryDelayMs(1<<40, new(maxSafeInteger), 40); got != maxSafeInteger {
		t.Fatalf("saturated delay = %d", got)
	}
}

type retryRecorder struct {
	scheduled     [][4]any
	attemptStarts int
	finished      [][3]any
	events        []string
}

func (recorder *retryRecorder) callbacks() RetryCallbacks {
	return RetryCallbacks{
		OnRetryScheduled: func(attempt, maxAttempts, delayMs int, errorMessage string) error {
			recorder.scheduled = append(recorder.scheduled, [4]any{attempt, maxAttempts, delayMs, errorMessage})
			recorder.events = append(recorder.events, fmt.Sprintf("retry:%d", attempt))
			return nil
		},
		OnRetryAttemptStart: func() error {
			recorder.attemptStarts++
			recorder.events = append(recorder.events, "attempt-start")
			return nil
		},
		OnRetryFinished: func(success bool, attempt int, finalError string) error {
			recorder.finished = append(recorder.finished, [3]any{success, attempt, finalError})
			return nil
		},
	}
}

var (
	retryDisabled = &RetryPolicy{Enabled: false, MaxRetries: 3}
	retryEnabled  = &RetryPolicy{Enabled: true, MaxRetries: 3}
)

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:120 — returns a successful response immediately without retrying.
func TestRetryAssistantCallReturnsSuccessImmediately(t *testing.T) {
	calls := 0
	response, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		calls++
		return retryFaux("ok", "", ""), nil
	}, retryEnabled, RetryCallbacks{})
	if err != nil || calls != 1 || !reflect.DeepEqual(response.Content, []AssistantContentBlock{TextContent{Text: "ok"}}) {
		t.Fatalf("response=%#v err=%v calls=%d", response, err, calls)
	}
}

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:127 — does not retry an aborted message.
func TestRetryAssistantCallDoesNotRetryAbortedMessage(t *testing.T) {
	recorder := &retryRecorder{}
	calls := 0
	response, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		calls++
		return retryFaux("", StopReasonAborted, ""), nil
	}, retryEnabled, recorder.callbacks())
	if err != nil || response.StopReason != StopReasonAborted || calls != 1 || len(recorder.scheduled) != 0 {
		t.Fatalf("response=%#v err=%v calls=%d scheduled=%v", response, err, calls, recorder.scheduled)
	}
}

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:136 — does not retry a non-retryable error (quota/billing).
func TestRetryAssistantCallDoesNotRetryNonRetryableError(t *testing.T) {
	recorder := &retryRecorder{}
	calls := 0
	response, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		calls++
		return errorFaux("insufficient_quota"), nil
	}, retryEnabled, recorder.callbacks())
	if err != nil || response.StopReason != StopReasonError || calls != 1 || len(recorder.scheduled) != 0 || len(recorder.finished) != 0 {
		t.Fatalf("response=%#v err=%v calls=%d recorder=%#v", response, err, calls, recorder)
	}
}

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:149 — retries a transient error up to maxRetries then returns the final error.
func TestRetryAssistantCallRetriesUpToMaxRetries(t *testing.T) {
	recorder := &retryRecorder{}
	calls := 0
	response, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		calls++
		return errorFaux("terminated"), nil
	}, retryEnabled, recorder.callbacks())
	if err != nil || response.StopReason != StopReasonError || calls != 4 || len(recorder.scheduled) != 3 {
		t.Fatalf("response=%#v err=%v calls=%d scheduled=%v", response, err, calls, recorder.scheduled)
	}
	if !reflect.DeepEqual(recorder.finished, [][3]any{{false, 3, "terminated"}}) {
		t.Fatalf("finished = %v", recorder.finished)
	}
}

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:160 — reports capped retry delays.
func TestRetryAssistantCallReportsCappedRetryDelays(t *testing.T) {
	recorder := &retryRecorder{}
	calls := 0
	policy := &RetryPolicy{Enabled: true, MaxRetries: 4, BaseDelayMs: 10, MaxAgentDelayMs: new(15)}
	if _, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		calls++
		if calls < 5 {
			return errorFaux("terminated"), nil
		}
		return retryFaux("recovered", "", ""), nil
	}, policy, recorder.callbacks()); err != nil {
		t.Fatal(err)
	}
	var delays []int
	for _, call := range recorder.scheduled {
		delays = append(delays, call[2].(int))
	}
	if !reflect.DeepEqual(delays, []int{10, 15, 15, 15}) {
		t.Fatalf("delays = %v", delays)
	}
}

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:177 — stops retrying once a call succeeds.
func TestRetryAssistantCallStopsOnceACallSucceeds(t *testing.T) {
	recorder := &retryRecorder{}
	calls := 0
	response, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		calls++
		if calls < 3 {
			return errorFaux("terminated"), nil
		}
		return retryFaux("recovered", "", ""), nil
	}, retryEnabled, recorder.callbacks())
	if err != nil || calls != 3 || !reflect.DeepEqual(response.Content, []AssistantContentBlock{TextContent{Text: "recovered"}}) {
		t.Fatalf("response=%#v err=%v calls=%d", response, err, calls)
	}
	if !reflect.DeepEqual(recorder.finished, [][3]any{{true, 2, ""}}) {
		t.Fatalf("finished = %v", recorder.finished)
	}
}

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:192 — reports an aborted retried call as unsuccessful.
func TestRetryAssistantCallReportsAbortedRetriedCallAsUnsuccessful(t *testing.T) {
	recorder := &retryRecorder{}
	calls := 0
	response, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		calls++
		if calls == 1 {
			return errorFaux("terminated"), nil
		}
		return retryFaux("", StopReasonAborted, ""), nil
	}, retryEnabled, recorder.callbacks())
	if err != nil || response.StopReason != StopReasonAborted || calls != 2 {
		t.Fatalf("response=%#v err=%v calls=%d", response, err, calls)
	}
	if !reflect.DeepEqual(recorder.finished, [][3]any{{false, 1, ""}}) {
		t.Fatalf("finished = %v", recorder.finished)
	}
}

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:207 — does not retry when policy is disabled.
func TestRetryAssistantCallDoesNotRetryWhenPolicyDisabled(t *testing.T) {
	recorder := &retryRecorder{}
	calls := 0
	response, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		calls++
		return errorFaux("terminated"), nil
	}, retryDisabled, recorder.callbacks())
	if err != nil || response.StopReason != StopReasonError || calls != 1 || len(recorder.scheduled) != 0 || len(recorder.finished) != 0 {
		t.Fatalf("response=%#v err=%v calls=%d recorder=%#v", response, err, calls, recorder)
	}
}

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:218 — emits onRetryAttemptStart after backoff before each retried call.
func TestRetryAssistantCallEmitsAttemptStartAfterBackoffBeforeEachRetry(t *testing.T) {
	recorder := &retryRecorder{}
	calls := 0
	response, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		recorder.events = append(recorder.events, fmt.Sprintf("produce:%d", calls))
		calls++
		if calls < 3 {
			return errorFaux("terminated"), nil
		}
		return retryFaux("recovered", "", ""), nil
	}, retryEnabled, recorder.callbacks())
	if err != nil || !reflect.DeepEqual(response.Content, []AssistantContentBlock{TextContent{Text: "recovered"}}) || len(recorder.scheduled) != 2 || recorder.attemptStarts != 2 {
		t.Fatalf("response=%#v err=%v recorder=%#v", response, err, recorder)
	}
	want := []string{"produce:0", "retry:1", "attempt-start", "produce:1", "retry:2", "attempt-start", "produce:2"}
	if !reflect.DeepEqual(recorder.events, want) {
		t.Fatalf("events = %v", recorder.events)
	}
}

// .upstream/v0.87.1/packages/ai/test/retry.test.ts:249 — aborts backoff sleep via signal, returns an aborted message, and emits onRetryFinished(false).
func TestRetryAssistantCallAbortsBackoffSleepViaContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := &retryRecorder{}
	var calls atomic.Int32
	policy := &RetryPolicy{Enabled: true, MaxRetries: 5, BaseDelayMs: 10_000}
	done := make(chan AssistantMessage, 1)
	go func() {
		response, err := RetryAssistantCall(ctx, func() (AssistantMessage, error) {
			calls.Add(1)
			return errorFaux("terminated"), nil
		}, policy, RetryCallbacks{
			OnRetryScheduled: func(int, int, int, string) error {
				cancel()
				return nil
			},
			OnRetryFinished: recorder.callbacks().OnRetryFinished,
		})
		if err != nil {
			t.Error(err)
		}
		done <- response
	}()
	select {
	case response := <-done:
		if response.StopReason != StopReasonAborted || response.ErrorMessage != "" || calls.Load() != 1 {
			t.Fatalf("response=%#v calls=%d", response, calls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("backoff sleep did not observe cancellation")
	}
	if !reflect.DeepEqual(recorder.finished, [][3]any{{false, 1, "terminated"}}) {
		t.Fatalf("finished = %v", recorder.finished)
	}
}

func TestRetryAssistantCallPropagatesCallbackAndProduceErrors(t *testing.T) {
	failure := fmt.Errorf("callback failed")
	if _, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		return errorFaux("terminated"), nil
	}, retryEnabled, RetryCallbacks{OnRetryScheduled: func(int, int, int, string) error { return failure }}); !errors.Is(err, failure) {
		t.Fatalf("callback err = %v", err)
	}
	if _, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		return AssistantMessage{}, failure
	}, retryEnabled, RetryCallbacks{}); !errors.Is(err, failure) {
		t.Fatalf("produce err = %v", err)
	}
}

func TestRetryAssistantCallMatchesNodeTimerOverflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	const delay = 2147483648
	calls := 0
	scheduled := 0
	response, err := RetryAssistantCall(ctx, func() (AssistantMessage, error) {
		calls++
		if calls == 1 {
			return errorFaux("terminated"), nil
		}
		return retryFaux("recovered", StopReasonStop, ""), nil
	}, &RetryPolicy{Enabled: true, MaxRetries: 1, BaseDelayMs: delay, MaxAgentDelayMs: new(delay)}, RetryCallbacks{
		OnRetryScheduled: func(_, _, delayMs int, _ string) error { scheduled = delayMs; return nil },
	})
	if err != nil || response.StopReason != StopReasonStop || calls != 2 || scheduled != delay {
		t.Fatalf("response=%#v err=%v calls=%d scheduled=%d", response, err, calls, scheduled)
	}
}

func TestRetryTimerDurationMatchesNodeBoundaries(t *testing.T) {
	for _, test := range []struct {
		delay int
		want  time.Duration
	}{
		{-1, time.Millisecond},
		{0, time.Millisecond},
		{1, time.Millisecond},
		{2147483647, 2147483647 * time.Millisecond},
		{2147483648, time.Millisecond},
		{9223372036855, time.Millisecond},
		{maxSafeInteger, time.Millisecond},
	} {
		if got := retryTimerDuration(test.delay); got != test.want {
			t.Errorf("timer(%d)=%v, want %v", test.delay, got, test.want)
		}
	}
}

func TestRetryDelayMsUsesSafeIntegerMagnitude(t *testing.T) {
	for _, test := range []struct{ base, attempt, want int }{
		{-2, 54, maxSafeInteger},
		{-1, 1, -1},
		{-2, 1025, maxSafeInteger},
		{2, -1 << 63, 2},
	} {
		if got := RetryDelayMs(test.base, new(maxSafeInteger), test.attempt); got != test.want {
			t.Errorf("base=%d attempt=%d delay=%d want=%d", test.base, test.attempt, got, test.want)
		}
	}
}

// Upstream RETRYABLE_PROVIDER_ERROR_PATTERN (packages/ai/src/utils/retry.ts)
// has no ECONNRESET or connection-reset entry, so a message carrying only Go's
// "connection reset by peer" text is not retried, as in pi-ai.
func TestConnectionResetTextIsNotRetryable(t *testing.T) {
	message := "github-copilot: request: read tcp 100.64.0.1:50817->140.82.112.22:443: read: connection reset by peer"
	if IsRetryableAssistantError(errorFaux(message)) {
		t.Fatalf("IsRetryableAssistantError(%q) = true; pi-ai's pattern list does not retry it", message)
	}
}
