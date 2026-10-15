package ai

import (
	"context"
	"testing"
)

// pi-mono #10656: Alibaba Model Studio (DashScope) answers token-rate throttling with HTTP 429 code insufficient_quota and "Allocated quota exceeded, please
// increase your quota limit." Pi's utils/retry.ts matches "quota exceeded", "insufficient_quota" and "billing" as non-retryable provider limits before it looks
// for a transient 429, so every one of these wordings ends the run after one call. PiG follows Pi (owner ruling 2026-10-10: byte parity over the retry).
func TestDashScopeQuotaWordingsAreNotRetriedAsPiDoesNot(t *testing.T) {
	for _, text := range []string{
		"429 Allocated quota exceeded, please increase your quota limit.",
		"429 Throttling.AllocationQuota: Allocated quota exceeded, please increase your quota limit.",
		`429 {"code":"Throttling.AllocationQuota","message":"insufficient_quota"}`,
		`429 {"code":"Throttling.AllocationQuota","message":"Free allocated quota exceeded."}`,
		"429 concurrency allocated quota exceeded. Retry shortly.",
		"429 Free allocated quota exceeded.",
		"429 You exceeded your current quota, please check your plan and billing details.",
		"429 insufficient_quota",
		"429 quota exceeded",
	} {
		if IsRetryableAssistantError(errorFaux(text)) {
			t.Errorf("IsRetryableAssistantError(%q) = true; Pi's retry.ts ends the run on a quota wording", text)
		}
	}

	recorder := &retryRecorder{}
	calls := 0
	response, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
		calls++
		return errorFaux("429 Allocated quota exceeded, please increase your quota limit."), nil
	}, retryEnabled, recorder.callbacks())
	if err != nil || response.StopReason != StopReasonError || calls != 1 || len(recorder.scheduled) != 0 {
		t.Fatalf("response=%#v err=%v calls=%d scheduled=%v, want one call and no retry", response, err, calls, recorder.scheduled)
	}
}
