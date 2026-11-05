// Ports packages/durable/test/harness-group-commit.test.ts: group commit and the scheduler (docs/group-commit.md §6):
// decisions read the scheduler's memory in regions that wait for the unpublished commits whose keys they read, memberships
// written at admission, outcomes, and close. The Go mappings of the module-level tasks, `flush()` and calls awaited later
// are in harness_group_commit_support_test.go.
//
// Go mappings. `harness as SessionImpl` is *harnessImpl, whose embedded Session line has ReadOnLine and AwaitTouch;
// `SCHEDULER_INDEX_SIZES` is the scheduler's indexSizes; upstream's `setTask(tx, record)` is the scheduler's setTask over
// the Session's transaction. Where upstream reads a task right after an `eventually` for another one, the test waits for the
// state it expects with eventually, as a state set by another goroutine has no order against the test's read. Where upstream
// reads a task as `running` and then acts on its handler, which Pi has started by then, the handler resolves a gate when it
// starts and the test waits for that: an invocation runs on its own goroutine (D181), and a Close that begins before its first
// step ends it before its handler runs.

package harness

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// gcOwnedByConversation is `{ ownership: { kind: "conversation" } }`.
var gcOwnedByConversation = durable.TaskOptions{Ownership: conversationOwned}

func gcOwnedByTask(owner durable.TaskId) durable.TaskOptions {
	return durable.TaskOptions{Ownership: scaleOwnedBy(owner)}
}

func gcConversationOf(owner durable.TaskId) durable.CreateConversationOptions {
	return durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}}
}

func gcMustConversation(t *testing.T, harness Harness, id durable.ConversationId) Conversation {
	t.Helper()
	conversation, err := harness.Conversation(testContext, id)
	if err != nil || conversation == nil {
		t.Fatalf("conversation %d: %v %v", id, conversation, err)
	}
	return conversation
}

// gcCreateHold commits the creation of the Hold task name in conversation.
func gcCreateHold(fixtures *gcFixtures, conversation Conversation, name string) func() (durable.TaskId, error) {
	return func() (durable.TaskId, error) {
		return durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, fixtures.hold, gcInput{Name: name}, gcOwnedByConversation)
		})
	}
}

// gcAppendNote commits a note entry to conversation.
func gcAppendNote(conversation Conversation) func() (any, error) {
	return func() (any, error) {
		return conversation.Commit(testContext, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: "note"})
		})
	}
}

func gcExpectCompleted(t *testing.T, harness Harness, id durable.TaskId) {
	t.Helper()
	if outcome := tkWaitOutcome(t, harness, id); outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("task %d ended %s, want completed", id, describeOutcome(outcome))
	}
}

func gcExpectStatus(t *testing.T, harness Harness, id durable.TaskId, want string) {
	t.Helper()
	if got := gcStatus(t, harness, id); got != want {
		t.Fatalf("task %d is %q, want %q", id, got, want)
	}
}

func TestGroupCommitSchedulerRegions(t *testing.T) {
	// harness-group-commit.test.ts:123 it("holds a live owner whose end races a creation below its ended tasks: live P, ended B and A, empty C")
	t.Run("holds a live owner whose end races a creation below its ended tasks: live P, ended B and A, empty C", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		type tree struct {
			p, b, a durable.TaskId
			c       durable.ConversationId
		}
		made := must(durable.Commit(testContext, root, func(tx durable.Tx) (tree, error) {
			p, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "p"}, gcOwnedByConversation)
			if err != nil {
				return tree{}, err
			}
			b, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "b"}, gcOwnedByTask(p))
			if err != nil {
				return tree{}, err
			}
			a, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "a"}, gcOwnedByTask(b))
			if err != nil {
				return tree{}, err
			}
			c, err := tx.CreateConversation(gcConversationOf(a))
			return tree{p, b, a, c.Id}, err
		}))
		fixtures.gate("a").resolve()
		tkWaitOutcome(t, harness, made.a)
		fixtures.gate("b").resolve()
		tkWaitOutcome(t, harness, made.b)
		// The creation of D in C is admitted and in flight when P commits its end.
		held := store.holdCommits()
		c := gcMustConversation(t, harness, made.c)
		creating := gcGo(gcCreateHold(fixtures, c, "d"))
		gcEntered(t, held)
		// P's decision reads what the creation writes, so it waits for its publication, holding the line.
		fixtures.gate("p").resolve()
		gcFlush(t)
		held.release()
		d := creating.wait(t)
		eventually(t, func() bool { return gcStatus(t, harness, made.p) == "completing" })
		fixtures.gate("d").resolve()
		tkWaitOutcome(t, harness, d)
		gcExpectCompleted(t, harness, made.p)
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:159 it("holds an owner whose end follows a creation below its child's pending end: live P, ending B, conversation C")
	t.Run("holds an owner whose end follows a creation below its child's pending end: live P, ending B, conversation C", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		type tree struct {
			p, b durable.TaskId
			c    durable.ConversationId
		}
		made := must(durable.Commit(testContext, root, func(tx durable.Tx) (tree, error) {
			p, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "p"}, gcOwnedByConversation)
			if err != nil {
				return tree{}, err
			}
			b, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "b"}, gcOwnedByTask(p))
			if err != nil {
				return tree{}, err
			}
			c, err := tx.CreateConversation(gcConversationOf(b))
			return tree{p, b, c.Id}, err
		}))
		c := gcMustConversation(t, harness, made.c)
		eventually(t, func() bool { return gcStatus(t, harness, made.b) == "running" })
		// A call in flight, so what follows is admitted and stays unpublished, in this order.
		held := store.holdCommits()
		blocker := gcGo(gcAppendNote(root))
		gcEntered(t, held)
		// B ends; then D is created in C while B still shows live; then P ends.
		fixtures.gate("b").resolve()
		gcFlush(t)
		creating := gcGo(gcCreateHold(fixtures, c, "d"))
		gcFlush(t)
		fixtures.gate("p").resolve()
		gcFlush(t)
		held.release()
		blocker.wait(t)
		d := creating.wait(t)
		// In line order D's creation precedes P's decision: P holds while D is live.
		eventually(t, func() bool { return gcStatus(t, harness, made.p) == "completing" })
		eventually(t, func() bool { return gcStatus(t, harness, d) == "running" })
		fixtures.gate("d").resolve()
		gcExpectCompleted(t, harness, made.p)
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:197 it("ends a child before its next phase when its owner's abort mark is unpublished at its step")
	t.Run("ends a child before its next phase when its owner's abort mark is unpublished at its step", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased(), fixtures.twoPhases.Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		type pair struct{ owner, child durable.TaskId }
		made := must(durable.Commit(testContext, root, func(tx durable.Tx) (pair, error) {
			owner, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "o"}, gcOwnedByConversation)
			if err != nil {
				return pair{}, err
			}
			child, err := durable.CreateTask(tx, fixtures.twoPhases, gcInput{Name: "t"}, gcOwnedByTask(owner))
			return pair{owner, child}, err
		}))
		eventually(t, func() bool {
			record, err := harness.GetTask(testContext, made.child)
			if err != nil || record == nil || record.State.Status != durable.TaskRunning || record.State.Checkpoint == nil {
				return false
			}
			members, _ := jsonOf(t, *record.State.Checkpoint).(map[string]any)
			return members["phase"] == "two"
		})
		held := store.holdCommits()
		marking := gcGo(func() (string, error) { return harness.AbortTask(testContext, made.owner) })
		gcEntered(t, held)
		// The child's phase returns: its step reads the intent set, which the unpublished mark changes.
		fixtures.gate("t.return").resolve()
		gcFlush(t)
		if ran := fixtures.ran("t.two"); ran != 0 {
			t.Fatalf("the child ran its next phase %d times before the owner's abort mark was published", ran)
		}
		held.release()
		if marked := marking.wait(t); marked != "marked" {
			t.Fatalf("abortTask returned %q, want marked", marked)
		}
		if outcome := tkWaitOutcome(t, harness, made.child); outcome.Status != durable.OutcomeAborted {
			t.Fatalf("the child ended %s, want aborted", describeOutcome(outcome))
		}
		if outcome := tkWaitOutcome(t, harness, made.owner); outcome.Status != durable.OutcomeAborted {
			t.Fatalf("the owner ended %s, want aborted", describeOutcome(outcome))
		}
		if ran := fixtures.ran("t.two"); ran != 0 {
			t.Fatalf("the child ran its next phase %d times", ran)
		}
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:226 it("refuses the second of two opposite waits admitted behind each other")
	t.Run("refuses the second of two opposite waits admitted behind each other", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.waits.Erased()})
		root := mustRoot(t, harness, nil)
		type pair struct{ x, y durable.TaskId }
		made := must(durable.Commit(testContext, root, func(tx durable.Tx) (pair, error) {
			x, err := durable.CreateTask(tx, fixtures.waits, gcInput{Name: "x"}, gcOwnedByConversation)
			if err != nil {
				return pair{}, err
			}
			y, err := durable.CreateTask(tx, fixtures.waits, gcInput{Name: "y"}, gcOwnedByConversation)
			return pair{x, y}, err
		}))
		// Each waits on the other, named in a memo.
		must(root.Commit(testContext, func(tx durable.Tx) (any, error) {
			// Every table read before the first write.
			x, err := tx.Task(made.x)
			if err != nil {
				return nil, err
			}
			y, err := tx.Task(made.y)
			if err != nil {
				return nil, err
			}
			records := []anyTaskRecord{*x, *y}
			records[0].Memos = map[string]durable.JsonValue{"on": float64(made.y)}
			records[1].Memos = map[string]durable.JsonValue{"on": float64(made.x)}
			internal := tx.(*session.Transaction)
			for _, record := range records {
				if err := setTask(internal, record); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}))
		harness.Resume()
		eventually(t, func() bool {
			return gcStatus(t, harness, made.x) == "running" && gcStatus(t, harness, made.y) == "running"
		})
		held := store.holdCommits()
		fixtures.gate("x").resolve()
		gcEntered(t, held)
		fixtures.gate("y").resolve()
		gcFlush(t)
		held.release()
		// Without the refusal both wait on each other for good: fail on the missing refusal, not on the wait.
		eventually(t, func() bool { return fixtures.refusal("y") != nil })
		tkWaitOutcome(t, harness, made.y)
		if err := fixtures.refusal("y"); err == nil || !strings.Contains(err.Error(), "wait cycle") {
			t.Fatalf("the wait of y was refused with %v, want a wait cycle", err)
		}
		if err := fixtures.refusal("x"); err != nil {
			t.Fatalf("the wait of x was refused: %v", err)
		}
		gcExpectCompleted(t, harness, made.x)
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:259 it("starts no invocation on a reservation before the reservation is published")
	t.Run("starts no invocation on a reservation before the reservation is published", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		root := mustRoot(t, harness, nil)
		id := must(gcCreateHold(fixtures, root, "r")())
		// The reservation's write is in flight: a scheduler commit with writes is never delivered before it is published.
		held := store.holdCommits()
		harness.Resume()
		gcEntered(t, held)
		gcFlush(t)
		if ran := fixtures.ran("r"); ran != 0 {
			t.Fatalf("the invocation started %d times on an unpublished reservation", ran)
		}
		held.release()
		eventually(t, func() bool { return fixtures.ran("r") == 1 })
		gcExpectStatus(t, harness, id, "running")
		fixtures.gate("r").resolve()
		tkWaitOutcome(t, harness, id)
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:281 it("runs a migration once although its reservation waits for an unpublished write of the task")
	t.Run("runs a migration once although its reservation waits for an unpublished write of the task", func(t *testing.T) {
		store := newControlledStorage()
		var migrations atomic.Int32
		v2 := durable.DefineTask(durable.TaskDefinition[gcInput, stepState, durable.JsonValue, any]{
			Name:    "gc.migrated",
			Version: 2,
			Initial: func(gcInput) stepState { return stepState{Phase: "hold"} },
			Migrate: func(input durable.JsonValue, checkpoint durable.JsonValue, _ int) (gcInput, stepState, error) {
				migrations.Add(1)
				typedInput, err := durable.FromJsonValue[gcInput](input)
				if err != nil {
					return gcInput{}, stepState{}, err
				}
				typedCheckpoint, err := durable.FromJsonValue[stepState](checkpoint)
				return typedInput, typedCheckpoint, err
			},
			Phases: map[string]durable.PhaseHandler[gcInput, stepState, durable.JsonValue, any]{
				"hold": func(ctx context.Context, _ gcRecord[stepState], runtime gcRuntime[stepState]) error {
					return gcCommit(ctx, runtime, gcCompleted[stepState])
				},
			},
			Abort: tkNoopAbort[gcInput, stepState, durable.JsonValue, any],
		})
		registry := CreateRegistry()
		mustInstall(t, registry, new(durable.Extension{Name: "v2", Tasks: []durable.AnyTask{v2.Erased()}}))
		harness, _, _ := openTasks(t, store, nil, openTasksOptions{registry: registry})
		root := mustRoot(t, harness, nil)
		id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, v2, gcInput{Name: "m"}, gcOwnedByConversation)
		}))
		// Stored at version 1, and written again by a commit that is in flight when the reservation reads it.
		held := store.holdCommits()
		writing := gcGo(func() (any, error) {
			return root.Commit(testContext, func(tx durable.Tx) (any, error) {
				record, err := tx.Task(id)
				if err != nil {
					return nil, err
				}
				stale := *record
				stale.Version = 1
				stale.Memos = map[string]durable.JsonValue{"touched": true}
				return nil, setTask(tx.(*session.Transaction), stale)
			})
		})
		gcEntered(t, held)
		harness.Resume()
		gcFlush(t)
		if got := migrations.Load(); got != 0 {
			t.Fatalf("%d migrations ran before the write was published, want 0", got)
		}
		held.release()
		writing.wait(t)
		gcExpectCompleted(t, harness, id)
		if got := migrations.Load(); got != 1 {
			t.Fatalf("%d migrations ran, want 1", got)
		}
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:324 it("reports a failed migration once although its reservation waits for an unpublished write of the task")
	t.Run("reports a failed migration once although its reservation waits for an unpublished write of the task", func(t *testing.T) {
		store := newControlledStorage()
		var migrations atomic.Int32
		v2 := durable.DefineTask(durable.TaskDefinition[gcInput, stepState, durable.JsonValue, any]{
			Name:    "gc.unmigratable",
			Version: 2,
			Initial: func(gcInput) stepState { return stepState{Phase: "hold"} },
			Migrate: func(durable.JsonValue, durable.JsonValue, int) (gcInput, stepState, error) {
				migrations.Add(1)
				return gcInput{}, stepState{}, errors.New("cannot migrate")
			},
			Phases: map[string]durable.PhaseHandler[gcInput, stepState, durable.JsonValue, any]{
				"hold": func(ctx context.Context, _ gcRecord[stepState], runtime gcRuntime[stepState]) error {
					return gcCommit(ctx, runtime, gcCompleted[stepState])
				},
			},
			Abort: tkNoopAbort[gcInput, stepState, durable.JsonValue, any],
		})
		registry := CreateRegistry()
		mustInstall(t, registry, new(durable.Extension{Name: "v2", Tasks: []durable.AnyTask{v2.Erased()}}))
		harness, _, reports := openTasks(t, store, nil, openTasksOptions{registry: registry})
		root := mustRoot(t, harness, nil)
		id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, v2, gcInput{Name: "m"}, gcOwnedByConversation)
		}))
		held := store.holdCommits()
		writing := gcGo(func() (any, error) {
			return root.Commit(testContext, func(tx durable.Tx) (any, error) {
				record, err := tx.Task(id)
				if err != nil {
					return nil, err
				}
				stale := *record
				stale.Version = 1
				return nil, setTask(tx.(*session.Transaction), stale)
			})
		})
		gcEntered(t, held)
		harness.Resume()
		gcFlush(t)
		held.release()
		writing.wait(t)
		eventually(t, func() bool { return migrations.Load() > 0 })
		failedReports := func() int {
			failed := 0
			for _, info := range reports.allInfos() {
				if info.Kind == durable.ReportMigrationFailed {
					failed++
				}
			}
			return failed
		}
		eventually(t, func() bool { return failedReports() > 0 })
		// Nothing runs the migration or reports it again.
		gcFlush(t)
		if got := migrations.Load(); got != 1 {
			t.Fatalf("%d migrations ran, want 1", got)
		}
		if failed := failedReports(); failed != 1 {
			t.Fatalf("%d migration_failed reports, want 1", failed)
		}
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:363 it("makes a reader of the roots wait for a creation under a root a pending end would unlist")
	t.Run("makes a reader of the roots wait for a creation under a root a pending end would unlist", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		// Root R is listed for its last live task, T.
		last := must(gcCreateHold(fixtures, root, "t")())
		eventually(t, func() bool { return gcStatus(t, harness, last) == "running" })
		published := gcFollowStoredTasks(harness, func(anyTaskRecord) bool { return true })
		// T's end is in flight; a creation under R is admitted after it, while R still shows listed.
		held := store.holdCommits()
		fixtures.gate("t").resolve()
		gcEntered(t, held)
		var created gcCreatedBy
		creating := gcGo(func() (durable.TaskId, error) {
			return durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
				id, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "d"}, gcOwnedByConversation)
				created.set(id)
				return id, err
			})
		})
		gcFlush(t)
		// A reader of the roots on the line (global idle's key) waits until the creation is published too.
		reading := gcReadAfter(harness, session.RootsKey, &created, published)
		held.release()
		creating.wait(t)
		if !reading.wait(t) {
			t.Fatal("the reader of the roots saw the creation unpublished")
		}
		fixtures.gate("d").resolve()
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:404 it("walks a creation's memberships past an ancestor whose pending end is unpublished")
	t.Run("walks a creation's memberships past an ancestor whose pending end is unpublished", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		// Live P, in the root conversation, owns the empty conversation C.
		type pair struct {
			p durable.TaskId
			c durable.ConversationId
		}
		made := must(durable.Commit(testContext, root, func(tx durable.Tx) (pair, error) {
			p, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "p"}, gcOwnedByConversation)
			if err != nil {
				return pair{}, err
			}
			c, err := tx.CreateConversation(gcConversationOf(p))
			return pair{p, c.Id}, err
		}))
		eventually(t, func() bool { return gcStatus(t, harness, made.p) == "running" })
		published := gcFollowStoredTasks(harness, func(anyTaskRecord) bool { return true })
		// P's end is in flight; a creation in C is admitted after it. Its chain must not stop at P, which shows live.
		conversation := gcMustConversation(t, harness, made.c)
		held := store.holdCommits()
		fixtures.gate("p").resolve()
		gcEntered(t, held)
		var created gcCreatedBy
		creating := gcGo(func() (durable.TaskId, error) {
			return durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.TaskId, error) {
				id, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "d"}, gcOwnedByConversation)
				created.set(id)
				return id, err
			})
		})
		gcFlush(t)
		reading := gcReadAfter(harness, session.RootsKey, &created, published)
		held.release()
		creating.wait(t)
		if !reading.wait(t) {
			t.Fatal("the reader of the roots saw the creation unpublished")
		}
		fixtures.gate("d").resolve()
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:446 it("reserves the live work of the owner above an ancestor whose end is pending, for a creation below it")
	t.Run("reserves the live work of the owner above an ancestor whose end is pending, for a creation below it", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		// Live G owns P, which owns the empty conversation C.
		type tree struct {
			g, p durable.TaskId
			c    durable.ConversationId
		}
		made := must(durable.Commit(testContext, root, func(tx durable.Tx) (tree, error) {
			g, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "g"}, gcOwnedByConversation)
			if err != nil {
				return tree{}, err
			}
			p, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "p"}, gcOwnedByTask(g))
			if err != nil {
				return tree{}, err
			}
			c, err := tx.CreateConversation(gcConversationOf(p))
			return tree{g, p, c.Id}, err
		}))
		eventually(t, func() bool { return gcStatus(t, harness, made.p) == "running" })
		published := gcFollowStoredTasks(harness, func(anyTaskRecord) bool { return true })
		// P's end is in flight; a creation in C is admitted after it, while P still shows live.
		conversation := gcMustConversation(t, harness, made.c)
		held := store.holdCommits()
		fixtures.gate("p").resolve()
		gcEntered(t, held)
		var created gcCreatedBy
		creating := gcGo(func() (durable.TaskId, error) {
			return durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.TaskId, error) {
				id, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "d"}, gcOwnedByConversation)
				created.set(id)
				return id, err
			})
		})
		gcFlush(t)
		// A reader of G's live work (as G's end decision is) waits until the creation is published too.
		reading := gcReadAfter(harness, session.OwnedKey(made.g), &created, published)
		held.release()
		creating.wait(t)
		if !reading.wait(t) {
			t.Fatal("the reader of G's live work saw the creation unpublished")
		}
		fixtures.gate("d").resolve()
		fixtures.gate("g").resolve()
		tkWaitOutcome(t, harness, made.g)
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:491 it("keeps the nodes a chain load installed for an unpublished creation through the sweep")
	t.Run("keeps the nodes a chain load installed for an unpublished creation through the sweep", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		type pair struct {
			b durable.TaskId
			c durable.ConversationId
		}
		made := must(durable.Commit(testContext, root, func(tx durable.Tx) (pair, error) {
			b, err := durable.CreateTask(tx, fixtures.hold, gcInput{Name: "b"}, gcOwnedByConversation)
			if err != nil {
				return pair{}, err
			}
			c, err := tx.CreateConversation(gcConversationOf(b))
			return pair{b, c.Id}, err
		}))
		fixtures.gate("b").resolve()
		tkWaitOutcome(t, harness, made.b)
		closeHarness(t, harness)
		// After reopen, B and C's edge are known only once a chain load reads them.
		store.Reopen()
		reopened, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		sizes := func() schedulerIndexSizes { return reopened.(*harnessImpl).tasks.indexSizes() }
		if got := sizes().settled; got != 0 {
			t.Fatalf("settled %d after reopen, want 0", got)
		}
		c := gcMustConversation(t, reopened, made.c)
		held := store.holdCommits()
		creating := gcGo(gcCreateHold(fixtures, c, "d"))
		gcEntered(t, held)
		// The admission loaded B; the sweep that load scheduled has run on the line since, and kept it.
		if err := reopened.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		gcFlush(t)
		if got := sizes().settled; got != 1 {
			t.Fatalf("settled %d while the creation is unpublished, want 1", got)
		}
		if got := sizes().pins; got <= 0 {
			t.Fatalf("pins %d while the creation is unpublished, want some", got)
		}
		held.release()
		creating.wait(t)
		// Published below a known chain: nothing is left to load, and no pin is left.
		if got := sizes().unloaded; got != 0 {
			t.Fatalf("unloaded %d after the creation is published, want 0", got)
		}
		if got := sizes().pins; got != 0 {
			t.Fatalf("pins %d after the creation is published, want 0", got)
		}
		idle := gcGo(func() (struct{}, error) { return struct{}{}, reopened.WaitForIdle(testContext) })
		if idle.settled(t) {
			t.Fatal("waitForIdle settled although D is live")
		}
		reopened.Resume()
		fixtures.gate("d").resolve()
		idle.wait(t)
		if err := reopened.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		closeHarness(t, reopened)
	})
}

// gcRaceMark advances probe name to n = 1 while the commit mark writes is in flight, and runs a reconcile pass meanwhile
// (upstream's raceMark).
func gcRaceMark(t *testing.T, fixtures *gcFixtures, store *controlledStorage, harness Harness, name string, mark func() error) {
	t.Helper()
	heldMark := store.holdCommits()
	marking := gcGo(func() (struct{}, error) { return struct{}{}, mark() })
	gcEntered(t, heldMark)
	var prepared atomic.Bool
	advancing := gcGo(func() (struct{}, error) {
		return struct{}{}, fixtures.probeRuntime(name).Commit(testContext, func(durable.Tx, gcRecord[gcProbeState]) (*gcNext[gcProbeState], error) {
			prepared.Store(true)
			return &gcNext[gcProbeState]{Status: durable.TaskRunning, Checkpoint: &gcProbeState{Phase: "hold", N: 1}}, nil
		})
	})
	eventually(t, prepared.Load)
	gcFlush(t)
	// Global idle schedules a reconcile pass, which reaches the line while both are unpublished.
	go func() { _ = harness.WaitForIdle(testContext) }()
	gcFlush(t)
	heldAdvance := store.holdCommits()
	heldMark.release()
	gcEntered(t, heldAdvance)
	gcFlush(t)
	heldAdvance.release()
	marking.wait(t)
	advancing.wait(t)
	gcFlush(t)
}

func TestGroupCommitRecordsADecisionCopies(t *testing.T) {
	// harness-group-commit.test.ts:608 it("marks a child in a cascade from its current record, not one its pending commit replaces")
	t.Run("marks a child in a cascade from its current record, not one its pending commit replaces", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.probe.Erased()})
		root := mustRoot(t, harness, nil)
		type pair struct{ parent, child durable.TaskId }
		made := must(durable.Commit(testContext, root, func(tx durable.Tx) (pair, error) {
			parent, err := durable.CreateTask(tx, fixtures.probe, gcInput{Name: "p"}, gcOwnedByConversation)
			if err != nil {
				return pair{}, err
			}
			child, err := durable.CreateTask(tx, fixtures.probe, gcInput{Name: "c"}, gcOwnedByTask(parent))
			return pair{parent, child}, err
		}))
		harness.Resume()
		eventually(t, func() bool { return fixtures.probeRuntime("c") != nil })
		gcFlush(t)
		gcRaceMark(t, fixtures, store, harness, "c", func() error {
			_, err := harness.AbortTask(testContext, made.parent)
			return err
		})
		eventually(t, func() bool {
			record, err := harness.GetTask(testContext, made.child)
			return err == nil && record != nil && record.AbortRequested
		})
		if n := gcCheckpoint(t, harness, made.child)["n"]; n != 1.0 {
			t.Fatalf("the child's checkpoint n is %v, want 1: the mark replaced the record its pending commit wrote", n)
		}
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:626 it("marks the other tasks of a failFast wait from their current records")
	t.Run("marks the other tasks of a failFast wait from their current records", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.probe.Erased(), fixtures.waiter.Erased()})
		root := mustRoot(t, harness, nil)
		must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, fixtures.waiter, struct{}{}, gcOwnedByConversation)
		}))
		harness.Resume()
		eventually(t, func() bool { return fixtures.probeRuntime("x") != nil && fixtures.probeRuntime("y") != nil })
		gcFlush(t)
		y := fixtures.probeRuntime("y").TaskId()
		// X fails while its commit is in flight; the pass that marks Y runs while Y's own commit is pending.
		gcRaceMark(t, fixtures, store, harness, "y", func() error {
			fixtures.gate("x").resolve()
			return nil
		})
		eventually(t, func() bool {
			record, err := harness.GetTask(testContext, y)
			return err == nil && record != nil && record.AbortRequested
		})
		if n := gcCheckpoint(t, harness, y)["n"]; n != 1.0 {
			t.Fatalf("Y's checkpoint n is %v, want 1: the mark replaced the record its pending commit wrote", n)
		}
		closeHarness(t, harness)
	})
}

// gcCountingStorage is controlled storage that counts its Task reads.
type gcCountingStorage struct {
	*controlledStorage
	taskReads atomic.Int64
}

func (store *gcCountingStorage) Task(ctx context.Context, id durable.TaskId) (*anyTaskRecord, error) {
	store.taskReads.Add(1)
	return store.controlledStorage.Task(ctx, id)
}

func TestGroupCommitReadsOnTheLine(t *testing.T) {
	// harness-group-commit.test.ts:646 it("reads no task record from Storage for more live and awaited tasks than the record cache's count")
	t.Run("reads no task record from Storage for more live and awaited tasks than the record cache's count", func(t *testing.T) {
		type none = struct{}
		type parentState struct {
			Phase string           `json:"phase"`
			On    []durable.TaskId `json:"on,omitempty"`
		}
		child := durable.DefineTask(durable.TaskDefinition[none, stepState, durable.JsonValue, any]{
			Name:    "gc.child",
			Version: 1,
			Initial: func(none) stepState { return stepState{Phase: "end"} },
			Phases: map[string]durable.PhaseHandler[none, stepState, durable.JsonValue, any]{
				"end": func(ctx context.Context, task durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
					return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[none, stepState, durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return completed[stepState, durable.JsonValue](float64(task.Id)), nil
					})
				},
			},
			Abort: tkNoopAbort[none, stepState, durable.JsonValue, any],
		})
		type parentRecord = durable.RunningTask[none, parentState, durable.JsonValue]
		parent := durable.DefineTask(durable.TaskDefinition[none, parentState, durable.JsonValue, any]{
			Name:    "gc.parent",
			Version: 1,
			Initial: func(none) parentState { return parentState{Phase: "spawn"} },
			Phases: map[string]durable.PhaseHandler[none, parentState, durable.JsonValue, any]{
				"spawn": func(ctx context.Context, task parentRecord, runtime durable.TaskRuntime[none, parentState, durable.JsonValue, any]) error {
					return runtime.Commit(ctx, func(tx durable.Tx, _ parentRecord) (*durable.NextTaskState[parentState, durable.JsonValue], error) {
						on := make([]durable.TaskId, 0, 300)
						for range 300 {
							id, err := durable.CreateTask(tx, child, none{}, gcOwnedByTask(task.Id))
							if err != nil {
								return nil, err
							}
							on = append(on, id)
						}
						return &durable.NextTaskState[parentState, durable.JsonValue]{Status: durable.TaskWaiting, Checkpoint: &parentState{Phase: "read", On: on}, On: on, Policy: durable.JoinAllSettled}, nil
					})
				},
				"read": func(ctx context.Context, task parentRecord, runtime durable.TaskRuntime[none, parentState, durable.JsonValue, any]) error {
					outcomes, err := runtime.Outcomes(ctx, task.State.Checkpoint.On)
					if err != nil {
						return err
					}
					sum := 0
					for _, outcome := range outcomes {
						if outcome.Status == durable.OutcomeCompleted {
							sum++
						}
					}
					return runtime.Commit(ctx, func(durable.Tx, parentRecord) (*durable.NextTaskState[parentState, durable.JsonValue], error) {
						return completed[parentState, durable.JsonValue](float64(sum)), nil
					})
				},
			},
			Abort: tkNoopAbort[none, parentState, durable.JsonValue, any],
		})
		store := &gcCountingStorage{controlledStorage: newControlledStorage()}
		harness, _, _ := openTasks(t, store, []durable.AnyTask{parent.Erased(), child.Erased()})
		root := mustRoot(t, harness, nil)
		id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, parent, none{}, gcOwnedByConversation)
		}))
		store.taskReads.Store(0)
		harness.Resume()
		expectOutcome(t, tkWaitOutcome(t, harness, id), outcomeCompleted(float64(300)))
		// Each change of a live task, and the parent's read of the outcomes it waited on, finds the record in memory.
		if reads := store.taskReads.Load(); reads != 0 {
			t.Fatalf("%d task reads from Storage, want 0", reads)
		}
		closeHarness(t, harness)
	})
}

func TestGroupCommitOutcomesAndPublicationThroughTheHarness(t *testing.T) {
	// harness-group-commit.test.ts:699 it("finds the submission an unpublished commit admitted with the same request ID")
	t.Run("finds the submission an unpublished commit admitted with the same request ID", func(t *testing.T) {
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, nil)
		root := mustRoot(t, harness, nil)
		requestId := "r1"
		draft := durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note", Data: map[string]any{}}, RequestId: &requestId}
		held := store.holdCommits()
		first := gcGo(func() (durable.Submission, error) { return root.Submit(testContext, draft) })
		gcEntered(t, held)
		second := gcGo(func() (durable.Submission, error) { return root.Submit(testContext, draft) })
		gcFlush(t)
		held.release()
		if got, want := second.wait(t).Id(), first.wait(t).Id(); got != want {
			t.Fatalf("the second submission is %d, want the first, %d", got, want)
		}
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:714 it("runs a wait continuation of one commit before the next commit of the same call is published")
	t.Run("runs a wait continuation of one commit before the next commit of the same call is published", func(t *testing.T) {
		fixtures := newGCFixtures()
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		root := mustRoot(t, harness, nil)
		other := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}))
		one := must(gcCreateHold(fixtures, root, "one")())
		two := must(gcCreateHold(fixtures, other, "two")())
		harness.Resume()
		eventually(t, func() bool {
			return gcStatus(t, harness, one) == "running" && gcStatus(t, harness, two) == "running"
		})
		ended := gcFollowStoredTasks(harness, func(record anyTaskRecord) bool { return record.State.Status == durable.TaskTerminal })
		// The continuation of the wait for one is a whole turn: the Session waits for it, up to the point the test says it is done,
		// before it publishes the next commit of the call (WithContinuation). The turn here ends when the test lets it, once it
		// checked that two's end is still unpublished.
		continuation, turn := session.WithContinuation(testContext)
		finish := deferred()
		var seen syncValue[bool]
		waited := gcGo(func() (struct{}, error) {
			_, err := harness.WaitForTask(continuation, one)
			if holdErr := finish.wait(testContext); err == nil {
				err = holdErr
			}
			seen.put(ended.has(two))
			turn.Entered()
			return struct{}{}, err
		})
		// The wait is registered, and the steps of the call before its wait have entered, before the commits are held: only what
		// follows the wake of the wait is held.
		tasks := harness.(*harnessImpl).tasks
		eventually(t, func() bool {
			turn.Entered()
			return len(tasks.taskWaiters.Keys()) > 0
		})
		gcFlush(t)
		turn.Entered()
		// Both end in one call, behind a commit in flight.
		held := store.holdCommits()
		blocker := gcGo(gcAppendNote(root))
		gcEntered(t, held)
		fixtures.gate("one").resolve()
		gcFlush(t)
		fixtures.gate("two").resolve()
		gcFlush(t)
		held.release()
		blocker.wait(t)
		// Everything that can run has run: the publication of one's end woke the wait, whose continuation is held, so two's end,
		// stored in the same call, is not published yet.
		gcFlush(t)
		if ended.has(two) {
			t.Fatal("the end of two was published before the continuation of the wait for one finished")
		}
		finish.resolve()
		waited.wait(t)
		tkWaitOutcome(t, harness, two)
		if endedTwo, ok := seen.get(); !ok || endedTwo {
			t.Fatalf("the continuation of the wait for one saw two's end published (set %v, value %v)", ok, endedTwo)
		}
		closeHarness(t, harness)
	})

	// harness-group-commit.test.ts:758 it("signals an invocation when it closes during a call in flight, and stores that call before Storage closes")
	t.Run("signals an invocation when it closes during a call in flight, and stores that call before Storage closes", func(t *testing.T) {
		store := newControlledStorage()
		entered := deferred()
		signalled := deferred()
		type none = struct{}
		listens := durable.DefineTask(durable.TaskDefinition[none, stepState, durable.JsonValue, any]{
			Name:    "gc.listens",
			Version: 1,
			Initial: func(none) stepState { return stepState{Phase: "wait"} },
			Phases: map[string]durable.PhaseHandler[none, stepState, durable.JsonValue, any]{
				"wait": func(_ context.Context, _ durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
					entered.resolve()
					err := abortedBy(runtime.Signal())
					signalled.resolve()
					return err
				},
			},
			Abort: tkNoopAbort[none, stepState, durable.JsonValue, any],
		})
		harness, _, _ := openTasks(t, store, []durable.AnyTask{listens.Erased()})
		root := mustRoot(t, harness, nil)
		id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, listens, none{}, gcOwnedByConversation)
		}))
		harness.Resume()
		eventually(t, func() bool { return gcStatus(t, harness, id) == "running" })
		gcAwaited(t, entered, "the handler of the running task did not start")
		held := store.holdCommits()
		entry := gcGo(gcAppendNote(root))
		gcEntered(t, held)
		closing := gcGo(func() (struct{}, error) { return struct{}{}, harness.Close(testContext) })
		gcAwaited(t, signalled, "the closing invocation was not signalled")
		if closing.settled(t) {
			t.Fatal("close settled while a call was in flight")
		}
		held.release()
		entry.wait(t)
		closing.wait(t)
		store.Reopen()
		reopened, _, _ := openTasks(t, store, []durable.AnyTask{listens.Erased()})
		conversation := gcMustConversation(t, reopened, root.Id())
		page := must(conversation.Entries(testContext, durable.EntryQuery{}, 10, nil))
		kinds := make([]string, 0, len(page.Items))
		found := false
		for _, item := range page.Items {
			kinds = append(kinds, item.Kind)
			found = found || item.Kind == "note"
		}
		if !found {
			t.Fatalf("the entries %v do not contain the note that was in flight at close", kinds)
		}
		// Close wrote the task it stopped back as pending.
		gcExpectStatus(t, reopened, id, "pending")
		closeHarness(t, reopened)
	})
}
