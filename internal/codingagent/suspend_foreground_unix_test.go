//go:build unix

package codingagent

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestSuspendWaiterKeepsWaitingAfterAParkedContinuation(t *testing.T) {
	ctx := t.Context()
	mode := &InteractiveMode{backgroundCtx: ctx, uiTaskCh: make(chan func(), 1)}
	calls := 0
	stop := mode.onSuspendContinue(func() error {
		calls++
		if calls == 1 {
			return errSuspendParked
		}
		return nil
	}, func() {})
	t.Cleanup(func() { stop(); mode.backgroundTasks.Wait() })
	for want := 1; want <= 2; want++ {
		if err := syscall.Kill(os.Getpid(), syscall.SIGCONT); err != nil {
			t.Fatal(err)
		}
		select {
		case apply := <-mode.uiTaskCh:
			apply()
		case <-time.After(5 * time.Second):
			t.Fatalf("SIGCONT %d never reached the owner loop: the waiter ended after a parked continuation", want)
		}
		if calls != want || mode.inputLoopErr != nil {
			t.Fatalf("after SIGCONT %d: continuation ran %d times, input loop error %v", want, calls, mode.inputLoopErr)
		}
	}
}
