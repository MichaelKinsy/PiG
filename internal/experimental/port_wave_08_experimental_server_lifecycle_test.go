package experimental

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestPortWave08ServerLifecycle(t *testing.T) {
	// packages/coding-agent/test/experimental-server-lifecycle.test.ts:9
	t.Run("holds a foreground server until explicit shutdown", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var retired atomic.Int32
			lifetime := NewServerLifetime(true)
			t.Cleanup(lifetime.Stop)
			lifetime.Start(func() { retired.Add(1) })
			time.Sleep(60_000 * time.Millisecond)
			synctest.Wait()
			if got := retired.Load(); got != 0 {
				t.Fatalf("retire calls = %d, want 0", got)
			}
			lifetime.Stop()
		})
	})
	// packages/coding-agent/test/experimental-server-lifecycle.test.ts:19
	t.Run("retires an automatic server if no first client arrives", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var retired atomic.Int32
			lifetime := NewServerLifetime(false)
			t.Cleanup(lifetime.Stop)
			lifetime.Start(func() { retired.Add(1) })
			time.Sleep(10_999 * time.Millisecond)
			synctest.Wait()
			if got := retired.Load(); got != 0 {
				t.Fatalf("retire calls before deadline = %d, want 0", got)
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			if got := retired.Load(); got != 1 {
				t.Fatalf("retire calls at deadline = %d, want 1", got)
			}
			lifetime.Stop()
		})
	})
	// packages/coding-agent/test/experimental-server-lifecycle.test.ts:31
	t.Run("requires both client and worker demand to disappear", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var retired atomic.Int32
			lifetime := NewServerLifetime(false)
			t.Cleanup(lifetime.Stop)
			lifetime.Start(func() { retired.Add(1) })
			lifetime.SetConnectionCount(1)
			lifetime.SetWorkerCount(1)
			lifetime.SetConnectionCount(0)
			time.Sleep(10_000 * time.Millisecond)
			synctest.Wait()
			if got := retired.Load(); got != 0 {
				t.Fatalf("retire calls while worker holds = %d, want 0", got)
			}
			lifetime.SetWorkerCount(0)
			time.Sleep(999 * time.Millisecond)
			synctest.Wait()
			if got := retired.Load(); got != 0 {
				t.Fatalf("retire calls before idle deadline = %d, want 0", got)
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			if got := retired.Load(); got != 1 {
				t.Fatalf("retire calls at idle deadline = %d, want 1", got)
			}
			lifetime.Stop()
		})
	})
}
