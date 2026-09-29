package codingagent

import (
	"testing"
	"time"
)

// Pi handleCtrlZ returns after process.kill returns. The original test inspects state after that return, then invokes its captured SIGCONT callback; no goroutine or pre-return kill barrier substitutes for that order.
func TestPairReview4SuspendReturnsBeforeContinue(t *testing.T) {
	var onContinue func() error
	started := false
	ops := suspendOperations{
		keepAlive:       func(time.Duration) func() { return func() {} },
		ignoreInterrupt: func() func() { return func() {} },
		continued:       func(continued func() error, _ func()) func() { onContinue = continued; return func() {} },
		stop:            func() {}, start: func() error { started = true; return nil }, requestRender: func() {}, kill: func(int) error { return nil },
	}
	if err := suspendTerminal(t.Context(), "linux", nil, ops); err != nil {
		t.Fatal(err)
	}
	if started || onContinue == nil {
		t.Fatal("handler must return with an inactive UI and registered continuation")
	}
	if err := onContinue(); err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("SIGCONT did not restart the UI")
	}
}
