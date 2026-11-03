package durabletest

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Pi testing/runner.ts:36 passes `testCase.timeoutMs` to runner.it, a test runner's per-case ceiling: a case that runs past it fails.
func TestRunCaseWithinFailsACaseThatRunsPastItsTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	err := runCaseWithin(20*time.Millisecond, func() error {
		<-release
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "timed out after 20ms") {
		t.Fatalf("a case that outlives its timeout = %v, want a timed out error", err)
	}
}

func TestRunCaseWithinReturnsTheCaseResultInsideItsTimeout(t *testing.T) {
	failure := errors.New("case failed")
	if err := runCaseWithin(time.Second, func() error { return failure }); !errors.Is(err, failure) {
		t.Errorf("err = %v, want the case's own error", err)
	}
	if err := runCaseWithin(time.Second, func() error { return nil }); err != nil {
		t.Errorf("err = %v", err)
	}
	// A zero timeout is the runner's default: the case runs unbounded.
	if err := runCaseWithin(0, func() error { time.Sleep(10 * time.Millisecond); return nil }); err != nil {
		t.Errorf("zero timeout: err = %v", err)
	}
}
