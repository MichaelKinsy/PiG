package experimental

import (
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestPortWave08WorkerLifecycle(t *testing.T) {
	const generation = "generation-1"
	create := func(t *testing.T) (*WorkerLifecycle, *atomic.Int32) {
		t.Helper()
		retired := new(atomic.Int32)
		lifecycle := NewWorkerLifecycle(WorkerLifecycleOptions{InitialServerConnectionID: new(generation), InitialDemandGraceMs: 100, OrphanDemandGraceMs: 200, OnRetire: func() { retired.Add(1) }})
		t.Cleanup(lifecycle.Close)
		return lifecycle, retired
	}
	demand := func(t *testing.T, l *WorkerLifecycle, g, a string, attached bool) {
		t.Helper()
		if err := l.SetDemand(g, a, attached); err != nil {
			t.Fatal(err)
		}
	}
	calls := func(t *testing.T, count *atomic.Int32, want int32) {
		t.Helper()
		if got := count.Load(); got != want {
			t.Fatalf("retire calls = %d, want %d", got, want)
		}
	}
	requireError := func(t *testing.T, err error, want string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:22
	t.Run("retires only after client demand and Harness activity are both gone", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, r := create(t)
			demand(t, l, generation, "attachment-1", true)
			l.OperationStarted("run", "main", "operation-1")
			demand(t, l, generation, "attachment-1", false)
			calls(t, r, 0)
			l.OperationStopped("run", "main", "operation-1")
			synctest.Wait()
			calls(t, r, 1)
			l.Close()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:36
	t.Run("retains the worker until every presentation attachment is released", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, r := create(t)
			demand(t, l, generation, "attachment-1", true)
			demand(t, l, generation, "attachment-2", true)
			demand(t, l, generation, "attachment-1", false)
			calls(t, r, 0)
			demand(t, l, generation, "attachment-2", false)
			calls(t, r, 1)
			l.Close()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:48
	t.Run("tracks a nested compaction independently from its enclosing run", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, r := create(t)
			demand(t, l, generation, "attachment-1", true)
			l.OperationStarted("run", "main", "operation-1")
			l.OperationStarted("compaction", "main", "operation-1")
			demand(t, l, generation, "attachment-1", false)
			l.OperationStopped("compaction", "main", "operation-1")
			calls(t, r, 0)
			l.OperationStopped("run", "main", "operation-1")
			calls(t, r, 1)
			l.Close()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:63 (both test.each rows)
	for _, kind := range []string{"compaction", "navigation"} {
		t.Run("clears suspended "+kind+" activity", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l, r := create(t)
				demand(t, l, generation, "attachment-1", true)
				l.OperationStarted(kind, "main", "operation-1")
				demand(t, l, generation, "attachment-1", false)
				l.OperationStopped(kind, "main", "operation-1")
				calls(t, r, 1)
				l.Close()
			})
		})
	}
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:75
	t.Run("does not retire while a demand acknowledgement holds reconciliation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, r := create(t)
			demand(t, l, generation, "attachment-1", true)
			release := l.HoldRetirement()
			demand(t, l, generation, "attachment-1", false)
			calls(t, r, 0)
			release()
			calls(t, r, 1)
			l.Close()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:87
	t.Run("holds retirement only for requests from the active attachment", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, r := create(t)
			demand(t, l, generation, "attachment-1", true)
			release, err := l.BeginRequest(generation, "attachment-1")
			if err != nil {
				t.Fatal(err)
			}
			demand(t, l, generation, "attachment-1", false)
			calls(t, r, 0)
			release()
			calls(t, r, 1)
			_, err = l.BeginRequest(generation, "attachment-1")
			requireError(t, err, "retiring")
			l.Close()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:100
	t.Run("rejects requests from stale generations and attachments", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, _ := create(t)
			demand(t, l, generation, "attachment-1", true)
			_, err := l.BeginRequest("stale", "attachment-1")
			requireError(t, err, "stale server generation")
			_, err = l.BeginRequest(generation, "wrong-attachment")
			requireError(t, err, "active attachment")
			l.Close()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:109
	t.Run("retains disconnected-generation demand for the orphan grace", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, r := create(t)
			demand(t, l, generation, "attachment-1", true)
			l.ServerDisconnected(generation)
			time.Sleep(199 * time.Millisecond)
			synctest.Wait()
			calls(t, r, 0)
			time.Sleep(time.Millisecond)
			synctest.Wait()
			calls(t, r, 1)
			l.Close()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:123
	t.Run("allows a replacement generation to retain the worker", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, r := create(t)
			demand(t, l, generation, "attachment-1", true)
			l.ServerDisconnected(generation)
			l.ServerConnected("generation-2")
			demand(t, l, "generation-2", "attachment-2", true)
			time.Sleep(200 * time.Millisecond)
			synctest.Wait()
			calls(t, r, 0)
			demand(t, l, "generation-2", "attachment-2", false)
			synctest.Wait()
			calls(t, r, 1)
			l.Close()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:139
	t.Run("retires a launched worker that never receives initial demand", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, r := create(t)
			time.Sleep(100 * time.Millisecond)
			synctest.Wait()
			calls(t, r, 1)
			l.Close()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:148
	t.Run("rejects demand after retirement has won the race", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, _ := create(t)
			time.Sleep(100 * time.Millisecond)
			synctest.Wait()
			requireError(t, l.SetDemand(generation, "attachment-1", true), "retiring")
			l.Close()
		})
	})
	// upstream: packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts:156
	t.Run("rejects demand from a stale server generation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, _ := create(t)
			requireError(t, l.SetDemand("stale", "attachment-1", true), "stale server generation")
			l.Close()
		})
	})
}
