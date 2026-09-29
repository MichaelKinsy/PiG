package experimental

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// A retained child owns os/exec.Wait. Looking up that capability does not infer process exit from the worker manager's independently changing PID map.
func experimentalWorkerChild(t *testing.T, pid int) *InternalProcess {
	t.Helper()
	resources := experimentalResourcesFor(t)
	resources.mu.Lock()
	defer resources.mu.Unlock()
	for _, child := range slices.Backward(resources.children) {

		if child.PID() == pid {
			return child
		}
	}
	t.Fatalf("worker %d has no captured native process exit authority", pid)
	return nil
}

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:686. Join actual process reaping, not manager retirement.
func waitProcessExited(t *testing.T, pid int) {
	t.Helper()
	if err := awaitProcessExit(t.Context(), experimentalWorkerChild(t, pid).Done(), processExitTimeout); err != nil {
		t.Fatalf("waiting for worker %d exit: %v", pid, err)
	}
}

// processExitTimeout is upstream's vitest testTimeout (packages/coding-agent/vitest.config.ts:10): a worker that never exits fails the test there with a targeted error instead of running to the go test deadline.
const processExitTimeout = 30 * time.Second

func awaitProcessExit(ctx context.Context, done <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return fmt.Errorf("process did not exit within %s", timeout)
	}
}

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:698. The signal does not substitute for a later independent manager-retirement or process-exit observation.
func killWorkerProcess(t *testing.T, pid int) {
	t.Helper()
	if err := experimentalWorkerChild(t, pid).cmd.Process.Kill(); err != nil {
		t.Fatalf("kill worker %d: %v", pid, err)
	}
}

func TestAwaitProcessExitFailsWithTargetedErrorWhenChildNeverExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		never := make(chan struct{})
		err := awaitProcessExit(t.Context(), never, processExitTimeout)
		if err == nil || err.Error() != "process did not exit within 30s" || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("awaitProcessExit = %v, want the targeted %q diagnostic", err, "process did not exit within 30s")
		}
		exited := make(chan struct{})
		close(exited)
		if err := awaitProcessExit(t.Context(), exited, processExitTimeout); err != nil {
			t.Fatalf("awaitProcessExit = %v for an exited child", err)
		}
	})
}
