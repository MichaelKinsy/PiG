package experimental

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// upstream: packages/coding-agent/src/experimental/server.ts:313-317 checks eligibility and calls retire() in one event-loop callback, and the retire installed at server.ts:684-686 begins runtime.close(), whose synchronous prefix is lifetime.stop() (server.ts:667). No setConnectionCount, setWorkerCount, or stop() can run between the check and that stop, so a retired generation is never revived by a later hold release.
func TestServerLifetimeRetirementIsAtomicWithEligibilityCheck(t *testing.T) {
	t.Run("retire begins with the lifetime already stopped", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lifetime := NewServerLifetime(false)
			var retired, stoppedAtRetire, timerAtRetire atomic.Int32
			lifetime.Start(func() {
				retired.Add(1)
				lifetime.mu.Lock()
				defer lifetime.mu.Unlock()
				if lifetime.stopped {
					stoppedAtRetire.Add(1)
				}
				if lifetime.retirementTimer != nil {
					timerAtRetire.Add(1)
				}
			})
			time.Sleep(11_000 * time.Millisecond)
			synctest.Wait()
			if retired.Load() != 1 || stoppedAtRetire.Load() != 1 || timerAtRetire.Load() != 0 {
				t.Fatalf("retired=%d stoppedAtRetire=%d timerAtRetire=%d, want 1/1/0", retired.Load(), stoppedAtRetire.Load(), timerAtRetire.Load())
			}
		})
	})
	t.Run("a hold released during retire cannot arm a second retirement", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lifetime := NewServerLifetime(false)
			var retired atomic.Int32
			lifetime.Start(func() {
				retired.Add(1)
				// The earliest moment a connection or worker can be observed after the decision.
				lifetime.SetConnectionCount(1)
				lifetime.SetWorkerCount(1)
				lifetime.SetConnectionCount(0)
				lifetime.SetWorkerCount(0)
			})
			time.Sleep(60_000 * time.Millisecond)
			synctest.Wait()
			if got := retired.Load(); got != 1 {
				t.Fatalf("retire calls = %d, want 1", got)
			}
		})
	})
}
