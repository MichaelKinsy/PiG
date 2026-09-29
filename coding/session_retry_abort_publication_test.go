package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent-session-retry-events.test.ts:77 aborts on the second failed assistant and requires final retry bookkeeping before Prompt returns.
func TestRetryFailureAbortClearsWillRetryBeforePromptReturns(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":0}}`}, fauxError("overloaded_error"), fauxError("overloaded_error"))
	errors := 0
	var ends []agent.AgentEndEvent
	var retryEnds []agent.AutoRetryEndEvent
	h.session.Subscribe(func(event agent.AgentEvent) {
		switch event := event.(type) {
		case agent.MessageEndEvent:
			if event.Message.Assistant != nil && event.Message.Assistant.StopReason == ai.StopReasonError {
				errors++
				if errors == 2 {
					h.session.RequestAbort()
				}
			}
		case agent.AgentEndEvent:
			ends = append(ends, event)
		case agent.AutoRetryEndEvent:
			retryEnds = append(retryEnds, event)
		}
	})
	if _, err := h.session.Prompt(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	if attempt := h.session.retryAttempt.Load(); attempt != 0 {
		t.Errorf("retryAttempt=%d want=0", attempt)
	}
	if len(ends) == 0 || ends[len(ends)-1].WillRetry {
		t.Errorf("last agent_end.willRetry: %v", ends)
	}
	if len(retryEnds) == 0 {
		t.Fatal("missing auto_retry_end")
	}
	end := retryEnds[len(retryEnds)-1]
	if end.Success || end.Attempt != 1 || end.FinalError != "Retry cancelled" {
		t.Errorf("last auto_retry_end=%+v", end)
	}
}
