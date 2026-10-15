package compaction

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func editErroredAttempt() agent.AgentMessage {
	failed := editAssistant("", ai.Usage{})
	failed.Assistant.Content = nil
	failed.Assistant.StopReason = ai.StopReasonError
	failed.Assistant.ErrorMessage = "WebSocket error"
	return failed
}

// Pi #10287: estimateProjectedContextTokens discards the usage anchor for any later context_edit, so omitting a
// zero-usage failed retry attempt (the context_edit _omitRecoveryAttempt appends) makes the footer and the compaction
// check re-estimate the whole history. An edit that only touches the entries after the anchor keeps it (D136).
func TestEstimateKeepsUsageAnchorWhenARetryOmissionEditsOnlyTheTrailingSuffix(t *testing.T) {
	e := newEditSession(t)
	e.message(editUser(strings.Repeat("history ", 5_000)))
	e.message(editAssistant("answer", ai.Usage{Input: 4_000, Output: 100, TotalTokens: 4_100}))
	e.message(editUser("next"))
	before := e.estimate()
	if before.UsageTokens != 4_100 {
		t.Fatalf("before = %+v, want the 4100 usage anchor", before)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		e.edit(e.message(editErroredAttempt()), nil)
		got := e.estimate()
		if got.UsageTokens != 4_100 || got.Tokens != before.Tokens {
			t.Fatalf("estimate after %d omitted failed attempts = %+v, want %+v", attempt, got, before)
		}
	}
}

func TestEstimateKeepsUsageAnchorWhenTrailingInputIsReplaced(t *testing.T) {
	e := newEditSession(t)
	e.message(editAssistant("answer", ai.Usage{Input: 4_000, Output: 100, TotalTokens: 4_100}))
	nextID := e.message(editUser("next"))
	e.edit(nextID, strings.Repeat("x", 400))
	got := e.estimate()
	if got.UsageTokens != 4_100 || got.TrailingTokens != 100 || got.Tokens != 4_200 {
		t.Fatalf("estimate = %+v, want usage 4100 + replaced trailing 100", got)
	}
}

func TestEstimateDropsUsageAnchorWhenTheAnchorOrEarlierInputIsEdited(t *testing.T) {
	for _, target := range []string{"earlier input", "anchor"} {
		t.Run(target, func(t *testing.T) {
			e := newEditSession(t)
			inputID := e.message(editUser(strings.Repeat("history ", 5_000)))
			anchorID := e.message(editAssistant("answer", ai.Usage{Input: 4_000, Output: 100, TotalTokens: 4_100}))
			e.message(editUser("next"))
			e.edit(map[string]string{"earlier input": inputID, "anchor": anchorID}[target], "short")
			if got := e.estimate(); got.UsageTokens != 0 {
				t.Fatalf("estimate = %+v, want the stale usage anchor dropped", got)
			}
		})
	}
}
