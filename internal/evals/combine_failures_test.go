package evals

import (
	"errors"
	"strings"
	"testing"
)

// packages/evals/src/harness.ts:448-455: a run error with cleanup errors is an AggregateError "Agent run failed and cleanup also failed." of
// all of them; one cleanup error alone is thrown as itself; several are an AggregateError "Agent cleanup failed."; a run error alone is
// itself.
// mutation-checked: wrapping a single cleanup error, or dropping the run error from the combined one, fails it.
func TestCombineFailuresAggregatesAsPiRunPiCodingAgent(t *testing.T) {
	run, first, second := errors.New("run"), errors.New("first"), errors.New("second")
	if got := combineFailures(nil, nil); got != nil {
		t.Fatalf("no failure = %v, want nil", got)
	}
	//nolint:errorlint // identity is the contract: Pi throws this error itself, so a wrapper must fail
	if got := combineFailures(run, nil); got != run {
		t.Fatalf("run error alone = %v, want it unchanged", got)
	}
	//nolint:errorlint // identity is the contract: Pi throws the one cleanup error itself, so a wrapper must fail
	if got := combineFailures(nil, []error{first}); got != first {
		t.Fatalf("one cleanup error = %v, want it unchanged", got)
	}
	several := combineFailures(nil, []error{first, second})
	if several == nil || !strings.HasPrefix(several.Error(), "Agent cleanup failed.") || !errors.Is(several, first) || !errors.Is(several, second) {
		t.Fatalf("two cleanup errors = %v, want \"Agent cleanup failed.\" holding both", several)
	}
	both := combineFailures(run, []error{first})
	if both == nil || !strings.HasPrefix(both.Error(), "Agent run failed and cleanup also failed.") || !errors.Is(both, run) || !errors.Is(both, first) {
		t.Fatalf("run and cleanup errors = %v, want \"Agent run failed and cleanup also failed.\" holding both", both)
	}
}
