package experimental

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// upstream: packages/coding-agent/src/experimental/session-worker.ts:292-304 sets #retiring and calls #onRetire in the same synchronous #reconcile, so setHarnessActive (session-worker.ts:279-282) cannot run between the retirement commit and the onRetire call.
func TestWorkerLifecycleRetireRunsInsideCommitCriticalSection(t *testing.T) {
	const generation = "generation-1"
	synctest.Test(t, func(t *testing.T) {
		var lifecycle *WorkerLifecycle
		var retired, lockFreeAtRetire atomic.Int32
		lifecycle = NewWorkerLifecycle(WorkerLifecycleOptions{InitialServerConnectionID: new(generation), InitialDemandGraceMs: 100, OrphanDemandGraceMs: 200, OnRetire: func() {
			retired.Add(1)
			if lifecycle.mu.TryLock() {
				lockFreeAtRetire.Add(1)
				lifecycle.mu.Unlock()
			}
		}})
		defer lifecycle.Close()
		if err := lifecycle.SetDemand(generation, "attachment-1", true); err != nil {
			t.Fatal(err)
		}
		if err := lifecycle.SetDemand(generation, "attachment-1", false); err != nil {
			t.Fatal(err)
		}
		if retired.Load() != 1 {
			t.Fatalf("retire calls = %d, want 1", retired.Load())
		}
		if got := lockFreeAtRetire.Load(); got != 0 {
			t.Fatalf("lifecycle state was unlocked while onRetire ran; SetHarnessActive could interleave (%d)", got)
		}
	})
}
