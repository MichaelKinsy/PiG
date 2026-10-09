package compaction

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"

	"github.com/MichaelKinsy/PiG/ai"
)

type retryCounters struct {
	scheduled    []int // attempts seen by OnRetryScheduled
	attemptStart int
	finished     int
}

func (c *retryCounters) callbacks() ai.RetryCallbacks {
	return ai.RetryCallbacks{
		OnRetryScheduled:    func(attempt, _, _ int, _ string) error { c.scheduled = append(c.scheduled, attempt); return nil },
		OnRetryAttemptStart: func() error { c.attemptStart++; return nil },
		OnRetryFinished:     func(bool, int, string) error { c.finished++; return nil },
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
		policy, callbacks := &ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}, c.callbacks()
		out, _, err := completeSimpleWithRetries(context.Background(), policy, callbacks, call)
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
		policy, callbacks := &ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}, c.callbacks()
		_, _, err := completeSimpleWithRetries(context.Background(), policy, callbacks, call)
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
		policy, callbacks := &ai.RetryPolicy{Enabled: false, MaxRetries: 3, BaseDelayMs: 0}, c.callbacks()
		_, _, err := completeSimpleWithRetries(context.Background(), policy, callbacks, call)
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
		policy, callbacks := &ai.RetryPolicy{Enabled: true, MaxRetries: 2, BaseDelayMs: 0}, c.callbacks()
		_, _, err := completeSimpleWithRetries(context.Background(), policy, callbacks, call)
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
		policy, callbacks := &ai.RetryPolicy{Enabled: true, MaxRetries: 5, BaseDelayMs: 30_000}, c.callbacks()
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()
		_, _, err := completeSimpleWithRetries(ctx, policy, callbacks, call)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if len(c.scheduled) != 1 || c.finished != 1 {
			t.Fatalf("scheduled=%d finished=%d, want 1/1", len(c.scheduled), c.finished)
		}
	})

	t.Run("nil opts runs a single unretried call", func(t *testing.T) {
		call, calls := scriptedCall([]string{"terminated"})
		_, _, err := completeSimpleWithRetries(context.Background(), nil, ai.RetryCallbacks{}, call)
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
	policy := &ai.RetryPolicy{Enabled: true, MaxRetries: 1, BaseDelayMs: 600_000, MaxAgentDelayMs: &limit}
	callbacks := ai.RetryCallbacks{OnRetryScheduled: func(_, _, delayMs int, _ string) error { delays = append(delays, delayMs); return nil }}
	if _, _, err := completeSimpleWithRetries(context.Background(), policy, callbacks, call); err != nil {
		t.Fatal(err)
	}
	if len(delays) != 1 || delays[0] != 1 {
		t.Fatalf("scheduled delays = %v, want [1]", delays)
	}
}

// packages/ai/src/utils/retry.ts retryAssistantCall classifies the failed response with isRetryableAssistantError: transient stream errors retry; quota, billing and unrelated errors return at once (regression #6647).
func TestCompleteSimpleWithRetriesClassifiesErrorsLikeUpstream(t *testing.T) {
	for _, tc := range []struct {
		message string
		retries bool
	}{
		{"terminated", true},
		{"socket connection was closed", true},
		{"stream ended before a terminal response event", true},
		{"insufficient_quota", false},
		{"Monthly usage limit reached", false},
		{"available balance is too low", false},
		{"some unrelated validation error", false},
	} {
		call, calls := scriptedCall([]string{tc.message, tc.message})
		_, _, err := completeSimpleWithRetries(context.Background(), &ai.RetryPolicy{Enabled: true, MaxRetries: 1, BaseDelayMs: 0}, ai.RetryCallbacks{}, call)
		if err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("%q: err = %v", tc.message, err)
		}
		if want := map[bool]int{true: 2, false: 1}[tc.retries]; *calls != want {
			t.Errorf("%q: calls = %d, want %d", tc.message, *calls, want)
		}
	}
}

// retryAssistantCall never retries an aborted response: a call that fails because the context was cancelled returns its own error without a retry or a finished callback.
func TestCompleteSimpleWithRetriesDoesNotRetryACancelledCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	var c retryCounters
	cause := errors.New("terminated")
	_, _, err := completeSimpleWithRetries(ctx, &ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}, c.callbacks(), func() (string, *ai.Usage, error) {
		calls++
		cancel()
		return "", nil, cause
	})
	if !errors.Is(err, cause) || calls != 1 || len(c.scheduled) != 0 || c.finished != 0 {
		t.Errorf("err=%v calls=%d scheduled=%d finished=%d", err, calls, len(c.scheduled), c.finished)
	}
}

// flakyCompleter fails its first call with a retryable stream error, then succeeds.
type flakyCompleter struct{ calls int }

func (f *flakyCompleter) CompleteSimple(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error) {
	f.calls++
	if f.calls == 1 {
		return "", nil, errors.New("terminated")
	}
	return "## Goal\nrecovered", nil, nil
}

// Every summarization entry point takes Pi's own `retry?: RetryPolicy` and `callbacks?: RetryCallbacks` (ai/src/utils/retry.ts):
// compaction.ts:624-625 compact/generateSummary, branch-summarization.ts:86-89 GenerateBranchSummaryOptions.retry/callbacks,
// bug-report.ts:325 retry. A retryable error is retried under the policy; a nil policy runs once. Compaction and branch summaries report
// the retry through the callbacks. The bug report takes no callbacks (bug-report.ts:315-327, 364), so its retry is not reported.
func TestSummarizationEntryPointsRetryUnderPiRetryPolicyAndReportThroughCallbacks(t *testing.T) {
	model := createSummaryModel(false, 8192, nil)
	policy := &ai.RetryPolicy{Enabled: true, MaxRetries: 2, BaseDelayMs: 0}
	entries := []struct {
		name    string
		reports bool
		run     func(c *flakyCompleter, retry *ai.RetryPolicy, callbacks ai.RetryCallbacks) error
	}{
		{"generateSummary (compaction.ts:624-625)", true, func(c *flakyCompleter, retry *ai.RetryPolicy, callbacks ai.RetryCallbacks) error {
			_, _, err := GenerateSummaryWithUsageUsing(t.Context(), summarizeThisMessages(), model, 2000, "", nil, "", "", "", c, nil, nil, retry, callbacks, "")
			return err
		}},
		{"generateBranchSummary (branch-summarization.ts:86-89)", true, func(c *flakyCompleter, retry *ai.RetryPolicy, callbacks ai.RetryCallbacks) error {
			result := GenerateBranchSummary(t.Context(), makeTestEntries(), GenerateBranchSummaryOptions{Model: model, Completer: c, Retry: retry, Callbacks: callbacks})
			if result.Error != "" {
				return errors.New(result.Error)
			}
			return nil
		}},
		{"generateBugReportSummary (bug-report.ts:325)", false, func(c *flakyCompleter, retry *ai.RetryPolicy, _ ai.RetryCallbacks) error {
			_, err := GenerateBugReportSummary(t.Context(), GenerateBugReportSummaryOptions{Messages: summarizeThisMessages(), Model: model, Completer: c, Retry: retry})
			return err
		}},
	}
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			var counters retryCounters
			flaky := &flakyCompleter{}
			if err := entry.run(flaky, policy, counters.callbacks()); err != nil {
				t.Fatalf("retried run failed: %v", err)
			}
			reported := 0
			if entry.reports {
				reported = 1
			}
			if flaky.calls != 2 || len(counters.scheduled) != reported || counters.attemptStart != reported || counters.finished != reported {
				t.Fatalf("calls=%d scheduled=%v attemptStart=%d finished=%d, want 2 calls and %d retry reports", flaky.calls, counters.scheduled, counters.attemptStart, counters.finished, reported)
			}
			once := &flakyCompleter{}
			if err := entry.run(once, nil, ai.RetryCallbacks{}); err == nil || once.calls != 1 {
				t.Fatalf("nil policy: err=%v calls=%d, want the first error after one call", err, once.calls)
			}
		})
	}
}
