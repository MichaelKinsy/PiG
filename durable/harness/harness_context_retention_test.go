// Ports the retention cases of the Pi 1.1.0 packages/durable/test/harness-context.test.ts ("keeps a conversation's
// context read across its tasks and for the retention period once idle", "drops an idle conversation's context read on
// a timer when nothing else runs", "drops a conversation's context read once idle with zero retention") for
// src/harness/scheduler.ts #contexts, #settleIdle, #scheduleExpiry and settings.contextRetentionMs.
//
// Pi counts the entry rows its storage scanned. PiG's derived-context cache (context_cache.go) extends a context from
// retained decoded entries instead of from scanEntries, so these cases observe the cache itself: a conversation's
// derived context is kept while the conversation is busy, kept for the retention period once idle, and dropped after.

package harness

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// retentionSetup opens a Harness over SQLite (whose reads the context cache serves) with a root transcript.
func retentionSetup(t *testing.T, retentionMs *float64, clock *clockAt) (opened tkOpened, probe durable.Task[durable.JsonValue, stepState, durable.JsonValue, any]) {
	t.Helper()
	probe = tkOneStep("test.context-probe", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		if _, err := runtime.Context(ctx, runtime.ConversationId(), nil); err != nil {
			return err
		}
		return tkComplete(ctx, runtime)
	})
	opened = tkOpenRoot(t, []durable.AnyTask{probe}, tkOptions{
		storage:  tkOpenSqlite(t, sqlitePath(t)),
		now:      clock.now,
		settings: func() *HarnessSettings { return &HarnessSettings{ContextRetentionMs: retentionMs} },
	})
	appendFirst(t, opened)
	opened.harness.Resume()
	return opened, probe
}

func appendFirst(t *testing.T, opened tkOpened) {
	t.Helper()
	if _, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.EntryRecord, error) {
		return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user("first")}})
	}); err != nil {
		t.Fatal(err)
	}
}

// runProbe runs the context-reading probe task to completion and waits for the conversation to go idle.
func runProbe(t *testing.T, opened tkOpened, task durable.AnyTask) {
	t.Helper()
	id := tkStart(t, opened.root, task)
	tkWaitOutcome(t, opened.harness, id)
	if err := opened.root.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
}

func (s *TaskScheduler) keptContext(id durable.ConversationId) (kept bool, idleSince *float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.keptContexts[id]
	if entry == nil {
		return false, nil
	}
	return s.contexts.has(id), entry.idleSince
}

func schedulerOf(opened tkOpened) *TaskScheduler { return opened.harness.(*harnessImpl).tasks }

func TestContextRetention(t *testing.T) {
	t.Run("keeps a conversation's context read for the retention period once idle", func(t *testing.T) {
		clock := &clockAt{}
		clock.set(1_000)
		opened, probe := retentionSetup(t, nil, clock)
		scheduler := schedulerOf(opened)
		runProbe(t, opened, probe)
		kept, idleSince := scheduler.keptContext(opened.root.Id())
		if !kept || idleSince == nil || *idleSince != 1_000 {
			t.Fatalf("after the run kept=%v idleSince=%v, want kept since 1000", kept, idleSince)
		}
		// A task that reads nothing, run after the retention period, settles the idle check: the context is dropped.
		clock.set(1_000 + 600_000)
		quiet := tkOneStep("test.quiet", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) })
		mustInstall(t, opened.registry, new(durable.Extension{Name: "quiet", Tasks: []durable.AnyTask{quiet}}))
		runProbe(t, opened, quiet)
		if kept, _ := scheduler.keptContext(opened.root.Id()); kept || scheduler.contexts.has(opened.root.Id()) {
			t.Fatal("the context outlived its retention period")
		}
		mustClose(t, opened.harness)
	})

	t.Run("keeps the context while a conversation is busy, whatever its age", func(t *testing.T) {
		clock := &clockAt{}
		clock.set(1_000)
		hold := deferred()
		reads := tkOneStep("test.busy-reader", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			if _, err := runtime.Context(ctx, runtime.ConversationId(), nil); err != nil {
				return err
			}
			clock.set(1_000 + 10*600_000)
			if err := hold.wait(ctx); err != nil {
				return err
			}
			return tkComplete(ctx, runtime)
		})
		other := tkOneStep("test.other", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) })
		opened := tkOpenRoot(t, []durable.AnyTask{reads, other}, tkOptions{storage: tkOpenSqlite(t, sqlitePath(t)), now: clock.now})
		appendFirst(t, opened)
		opened.harness.Resume()
		readerId := tkStart(t, opened.root, reads)
		scheduler := schedulerOf(opened)
		eventually(t, func() bool { kept, _ := scheduler.keptContext(opened.root.Id()); return kept })
		// Another task settles while the reader still runs: the conversation is busy, so nothing expires.
		tkWaitOutcome(t, opened.harness, tkStart(t, opened.root, other))
		if kept, idleSince := scheduler.keptContext(opened.root.Id()); !kept || idleSince != nil {
			t.Fatalf("a busy conversation kept=%v idleSince=%v, want kept and not idle", kept, idleSince)
		}
		hold.resolve()
		tkWaitOutcome(t, opened.harness, readerId)
		mustClose(t, opened.harness)
	})

	t.Run("drops an idle conversation's context read on a timer when nothing else runs", func(t *testing.T) {
		clock := &clockAt{}
		clock.set(1_000)
		opened, probe := retentionSetup(t, new(50.0), clock)
		scheduler := schedulerOf(opened)
		runProbe(t, opened, probe)
		if kept, _ := scheduler.keptContext(opened.root.Id()); !kept {
			t.Fatal("the idle conversation's context was not kept")
		}
		// The period passes on the Harness clock; the timer then runs the idle check with no task activity.
		clock.set(1_000 + 50)
		eventually(t, func() bool { kept, _ := scheduler.keptContext(opened.root.Id()); return !kept })
		if scheduler.contexts.has(opened.root.Id()) {
			t.Fatal("the timer dropped the retention entry but not the derived context")
		}
		mustClose(t, opened.harness)
	})

	t.Run("drops a conversation's context read once idle with zero retention", func(t *testing.T) {
		clock := &clockAt{}
		clock.set(1_000)
		opened, probe := retentionSetup(t, new(0.0), clock)
		scheduler := schedulerOf(opened)
		runProbe(t, opened, probe)
		if kept, _ := scheduler.keptContext(opened.root.Id()); kept || scheduler.contexts.has(opened.root.Id()) {
			t.Fatal("a zero retention period kept the context of an idle conversation")
		}
		mustClose(t, opened.harness)
	})

	t.Run("starts or continues the retention period of another, idle conversation that a task reads", func(t *testing.T) {
		for _, retentionMs := range []float64{0, 600_000} {
			clock := &clockAt{}
			clock.set(1_000)
			var other atomic.Int64
			var scheduler atomic.Pointer[TaskScheduler]
			type kept struct {
				kept      bool
				idleSince *float64
			}
			var afterRead atomic.Pointer[kept]
			reader := tkOneStep("test.cross-reader", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				if _, err := runtime.Context(ctx, durable.ConversationId(other.Load()), nil); err != nil {
					return err
				}
				// Observed before the read returns to the task: no timer can have run yet for a period that starts now.
				found, idleSince := scheduler.Load().keptContext(durable.ConversationId(other.Load()))
				afterRead.Store(&kept{found, idleSince})
				return tkComplete(ctx, runtime)
			})
			opened := tkOpenRoot(t, []durable.AnyTask{reader}, tkOptions{
				storage:  tkOpenSqlite(t, sqlitePath(t)),
				now:      clock.now,
				settings: func() *HarnessSettings { return &HarnessSettings{ContextRetentionMs: &retentionMs} },
			})
			bystander, err := opened.harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
			if err != nil {
				t.Fatal(err)
			}
			other.Store(int64(bystander.Id()))
			scheduler.Store(schedulerOf(opened))
			if _, err := durable.Commit(testContext, bystander, func(tx durable.Tx) (durable.EntryRecord, error) {
				return tx.AppendEntry(bystander.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user("hello")}})
			}); err != nil {
				t.Fatal(err)
			}
			opened.harness.Resume()
			runProbe(t, opened, reader)
			observed := afterRead.Load()
			if wantKept := retentionMs > 0; observed.kept != wantKept || (wantKept && (observed.idleSince == nil || *observed.idleSince != 1_000)) {
				t.Fatalf("retention %v: bystander context kept=%v idleSince=%v", retentionMs, observed.kept, observed.idleSince)
			}
			mustClose(t, opened.harness)
		}
	})

	t.Run("drops every kept context at close and stops the expiry timer", func(t *testing.T) {
		clock := &clockAt{}
		clock.set(1_000)
		opened, probe := retentionSetup(t, nil, clock)
		scheduler := schedulerOf(opened)
		runProbe(t, opened, probe)
		scheduler.mu.Lock()
		armed := scheduler.contextExpiry != nil
		scheduler.mu.Unlock()
		if !armed {
			t.Fatal("no expiry timer was armed for an idle kept context")
		}
		mustClose(t, opened.harness)
		scheduler.mu.Lock()
		defer scheduler.mu.Unlock()
		if scheduler.contextExpiry != nil || len(scheduler.keptContexts) != 0 || scheduler.contexts.has(opened.root.Id()) {
			t.Fatal("close left a kept context or an expiry timer")
		}
	})
}
