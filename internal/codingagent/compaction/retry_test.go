package compaction

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// isRetryableTest classifies "terminated" as transient and "insufficient_quota"
// as non-retryable, mirroring the coding-layer classifier the harness injects.
func isRetryableTest(msg string) bool {
	if strings.Contains(msg, "insufficient_quota") {
		return false
	}
	return strings.Contains(msg, "terminated")
}

type retryCounters struct {
	scheduled    []int // attempts seen by OnRetryScheduled
	attemptStart int
	finished     int
}

func (c *retryCounters) callbacks() RetryCallbacks {
	return RetryCallbacks{
		OnRetryScheduled:    func(attempt, _, _ int, _ string) { c.scheduled = append(c.scheduled, attempt) },
		OnRetryAttemptStart: func() { c.attemptStart++ },
		OnRetryFinished:     func() { c.finished++ },
	}
}

// scriptedCall returns a call func yielding the scripted results in order, with
// a counter of how many times it ran. err strings become errors; "" is success.
func scriptedCall(script []string) (func() (string, *ai.Usage, error), *int) {
	n := 0
	call := func() (string, *ai.Usage, error) {
		i := n
		n++
		if i >= len(script) {
			i = len(script) - 1
		}
		if script[i] == "" {
			return "recovered summary", nil, nil
		}
		return "", nil, errors.New(script[i])
	}
	return call, &n
}

func TestCompleteSimpleWithRetries(t *testing.T) {
	t.Run("retries transient error then succeeds", func(t *testing.T) {
		call, calls := scriptedCall([]string{"terminated", "terminated", ""})
		var c retryCounters
		opts := &RetryOptions{
			Policy:      RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0},
			IsRetryable: isRetryableTest,
			Callbacks:   c.callbacks(),
		}
		out, _, err := completeSimpleWithRetries(context.Background(), opts, call)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "recovered summary") {
			t.Fatalf("output = %q", out)
		}
		if *calls != 3 {
			t.Fatalf("calls = %d, want 3 (1 initial + 2 retries)", *calls)
		}
		if len(c.scheduled) != 2 || c.scheduled[0] != 1 || c.scheduled[1] != 2 {
			t.Fatalf("scheduled attempts = %v, want [1 2]", c.scheduled)
		}
		if c.attemptStart != 2 || c.finished != 1 {
			t.Fatalf("attemptStart=%d finished=%d, want 2 and 1", c.attemptStart, c.finished)
		}
	})

	t.Run("does not retry a non-retryable error", func(t *testing.T) {
		call, calls := scriptedCall([]string{"insufficient_quota"})
		var c retryCounters
		opts := &RetryOptions{
			Policy:      RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0},
			IsRetryable: isRetryableTest,
			Callbacks:   c.callbacks(),
		}
		_, _, err := completeSimpleWithRetries(context.Background(), opts, call)
		if err == nil || !strings.Contains(err.Error(), "insufficient_quota") {
			t.Fatalf("err = %v, want insufficient_quota", err)
		}
		if *calls != 1 || len(c.scheduled) != 0 || c.finished != 0 {
			t.Fatalf("calls=%d scheduled=%d finished=%d, want 1/0/0", *calls, len(c.scheduled), c.finished)
		}
	})

	t.Run("does not retry when disabled", func(t *testing.T) {
		call, calls := scriptedCall([]string{"terminated"})
		var c retryCounters
		opts := &RetryOptions{
			Policy:      RetryPolicy{Enabled: false, MaxRetries: 3, BaseDelayMs: 0},
			IsRetryable: isRetryableTest,
			Callbacks:   c.callbacks(),
		}
		_, _, err := completeSimpleWithRetries(context.Background(), opts, call)
		if err == nil || !strings.Contains(err.Error(), "terminated") {
			t.Fatalf("err = %v, want terminated", err)
		}
		if *calls != 1 || len(c.scheduled) != 0 {
			t.Fatalf("calls=%d scheduled=%d, want 1/0", *calls, len(c.scheduled))
		}
	})

	t.Run("stops after maxRetries and reports failure", func(t *testing.T) {
		call, calls := scriptedCall([]string{"terminated", "terminated", "terminated"})
		var c retryCounters
		opts := &RetryOptions{
			Policy:      RetryPolicy{Enabled: true, MaxRetries: 2, BaseDelayMs: 0},
			IsRetryable: isRetryableTest,
			Callbacks:   c.callbacks(),
		}
		_, _, err := completeSimpleWithRetries(context.Background(), opts, call)
		if err == nil || !strings.Contains(err.Error(), "terminated") {
			t.Fatalf("err = %v, want terminated", err)
		}
		if *calls != 3 { // 1 initial + 2 retries
			t.Fatalf("calls = %d, want 3", *calls)
		}
		if len(c.scheduled) != 2 || c.finished != 1 {
			t.Fatalf("scheduled=%d finished=%d, want 2/1", len(c.scheduled), c.finished)
		}
	})

	t.Run("aborts in-flight backoff via ctx", func(t *testing.T) {
		call, _ := scriptedCall([]string{"terminated", "terminated", "terminated"})
		var c retryCounters
		opts := &RetryOptions{
			Policy:      RetryPolicy{Enabled: true, MaxRetries: 5, BaseDelayMs: 30_000},
			IsRetryable: isRetryableTest,
			Callbacks:   c.callbacks(),
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()
		_, _, err := completeSimpleWithRetries(ctx, opts, call)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if len(c.scheduled) != 1 || c.finished != 1 {
			t.Fatalf("scheduled=%d finished=%d, want 1/1", len(c.scheduled), c.finished)
		}
	})

	t.Run("nil opts runs a single unretried call", func(t *testing.T) {
		call, calls := scriptedCall([]string{"terminated"})
		_, _, err := completeSimpleWithRetries(context.Background(), nil, call)
		if err == nil || *calls != 1 {
			t.Fatalf("err=%v calls=%d, want error and 1 call", err, *calls)
		}
	})
}

// Upstream retryDelayMs caps each summarization retry delay at
// maxAgentDelayMs.
func TestCompleteSimpleWithRetriesCapsDelay(t *testing.T) {
	call, _ := scriptedCall([]string{"terminated", ""})
	var delays []int
	limit := 1
	opts := &RetryOptions{
		Policy:      RetryPolicy{Enabled: true, MaxRetries: 1, BaseDelayMs: 600_000, MaxAgentDelayMs: &limit},
		IsRetryable: isRetryableTest,
		Callbacks:   RetryCallbacks{OnRetryScheduled: func(_, _, delayMs int, _ string) { delays = append(delays, delayMs) }},
	}
	if _, _, err := completeSimpleWithRetries(context.Background(), opts, call); err != nil {
		t.Fatal(err)
	}
	if len(delays) != 1 || delays[0] != 1 {
		t.Fatalf("scheduled delays = %v, want [1]", delays)
	}
}
