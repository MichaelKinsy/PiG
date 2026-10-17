// Ports packages/durable/test/harness-scheduler-retention.test.ts (upstream ea448f45): scheduler memory is bounded by live
// work. An ended task or a conversation edge is kept only while live work is below it, and a chain dropped that way is
// loaded again from storage when work appears below it.

package harness

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func retentionSizes(harness Harness) schedulerIndexSizes {
	return harness.(*harnessImpl).tasks.indexSizes()
}

// retentionCycle runs, under live parent: tool task T owning conversation C, a task in C; all of them end. It returns C.
func retentionCycle(t *testing.T, fixtures *ownFixtures, opened ownOpened, parent durable.TaskId, n int) durable.ConversationId {
	t.Helper()
	type made struct {
		tool, inner  durable.TaskId
		conversation durable.ConversationId
	}
	tool, inner := fmt.Sprintf("tool%d", n), fmt.Sprintf("inner%d", n)
	tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
		toolId, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: tool}, durable.TaskOptions{Ownership: scaleOwnedBy(parent)})
		if err != nil {
			return made{}, err
		}
		conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: toolId}})
		if err != nil {
			return made{}, err
		}
		innerId, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: inner}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &conversation.Id})
		return made{toolId, innerId, conversation.Id}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	fixtures.open(inner, "completed")
	fixtures.open(tool, "completed")
	ownExpectOutcome(t, opened.harness, tree.inner, durable.OutcomeCompleted)
	ownExpectOutcome(t, opened.harness, tree.tool, durable.OutcomeCompleted)
	return tree.conversation
}

type rejectedInput struct{}

// retentionRejected holds until the gate "rejected" opens, then tries one terminal commit that creates a held task in
// conversation *into and a child of its own, which the Transaction rejects as the task is ending. It records the rejection
// and holds. Upstream passes the conversation through the task input, set after creation with a raw setTask; here the test
// sets *into before it opens the gate.
func retentionRejected(fixtures *ownFixtures, into *atomic.Int64, rejections chan<- error) durable.Task[rejectedInput, stepState, durable.JsonValue, any] {
	type record = durable.RunningTask[rejectedInput, stepState, durable.JsonValue]
	type runtime = durable.TaskRuntime[rejectedInput, stepState, durable.JsonValue, any]
	return durable.DefineTask(durable.TaskDefinition[rejectedInput, stepState, durable.JsonValue, any]{
		Name:    "test.retention.rejected",
		Version: 1,
		Initial: func(rejectedInput) stepState { return stepState{Phase: "hold"} },
		Phases: map[string]durable.PhaseHandler[rejectedInput, stepState, durable.JsonValue, any]{
			"hold": func(ctx context.Context, task record, runtime runtime) error {
				select {
				case <-fixtures.gate("rejected").done:
				case <-runtime.Signal().Done():
					return context.Cause(runtime.Signal())
				}
				err := runtime.Commit(ctx, func(tx durable.Tx, _ record) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					conversation := durable.ConversationId(into.Load())
					if _, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "staged"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &conversation}); err != nil {
						return nil, err
					}
					if _, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "child"}, durable.TaskOptions{Ownership: scaleOwnedBy(task.Id)}); err != nil {
						return nil, err
					}
					return completed[stepState, durable.JsonValue](nil), nil
				})
				rejections <- err
				<-runtime.Signal().Done()
				return context.Cause(runtime.Signal())
			},
		},
		Abort: func(ctx context.Context, _ record, runtime runtime) error {
			return runtime.Commit(ctx, func(durable.Tx, record) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
}

func TestSchedulerRetention(t *testing.T) {
	t.Run("forgets ended subagent chains once their work ends", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		parent, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "parent"}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		retentionCycle(t, fixtures, opened, parent, 0)
		baseline := retentionSizes(opened.harness)
		for n := 1; n <= 200; n++ {
			retentionCycle(t, fixtures, opened, parent, n)
		}
		if got := retentionSizes(opened.harness); got != baseline {
			t.Fatalf("index sizes after 200 more ended subagent cycles %+v, want the baseline %+v", got, baseline)
		}
		fixtures.open("parent", "completed")
		ownExpectOutcome(t, opened.harness, parent, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	// Live P -> ended B -> ended A -> conversation C, empty: forgotten. The work later created in C holds P, whose chain
	// is loaded again from storage.
	t.Run("holds a live owner for work later created in a forgotten conversation, loading its chain again", func(t *testing.T) {
		fixtures := newOwnFixtures()
		store := &readCounting{Storage: storage.NewMemoryStorage()}
		opened := ownOpenHarness(t, fixtures, store)
		type made struct {
			p, b, a      durable.TaskId
			conversation durable.ConversationId
		}
		tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
			p, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "p"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return made{}, err
			}
			b, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "b"}, durable.TaskOptions{Ownership: scaleOwnedBy(p)})
			if err != nil {
				return made{}, err
			}
			a, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "a"}, durable.TaskOptions{Ownership: scaleOwnedBy(b)})
			if err != nil {
				return made{}, err
			}
			conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: a}})
			return made{p, b, a, conversation.Id}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("a", "completed")
		fixtures.open("b", "completed")
		ownExpectOutcome(t, opened.harness, tree.b, durable.OutcomeCompleted)
		ownWaitUntil(t, func() bool { return retentionSizes(opened.harness).dropQueue == 0 })
		before := retentionSizes(opened.harness)
		loaded := store.reads.Load()
		late := ownConversation(t, opened.harness, tree.conversation)
		d, err := durable.Commit(testContext, late, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "d"}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("p", "completed")
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, tree.p).Status == durable.TaskCompleting })
		// The forgotten chain is read from storage again.
		if got := store.reads.Load(); got <= loaded {
			t.Fatalf("task and conversation reads %d after the work below the forgotten chain started, want more than %d", got, loaded)
		}
		fixtures.open("d", "completed")
		ownExpectOutcome(t, opened.harness, d, durable.OutcomeCompleted)
		ownExpectOutcome(t, opened.harness, tree.p, durable.OutcomeCompleted)
		ownWaitUntil(t, func() bool { return retentionSizes(opened.harness).dropQueue == 0 })
		if got := retentionSizes(opened.harness); got.settled > before.settled {
			t.Fatalf("settled %d after the work ended, want at most %d", got.settled, before.settled)
		}
		mustClose(t, opened.harness)
	})

	t.Run("drops what a rejected commit's chain load read, without a later publication", func(t *testing.T) {
		fixtures := newOwnFixtures()
		var into atomic.Int64
		rejections := make(chan error, 1)
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{fixtures.hold.Erased(), retentionRejected(fixtures, &into, rejections).Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		// Live P owns ended B, which owns conversation C: forgotten. P's terminal commit stages work in C, which loads C's
		// chain, and a child of P, which the Transaction rejects, as P is ending.
		type made struct {
			p, b durable.TaskId
			c    durable.ConversationId
		}
		tree, err := durable.Commit(testContext, root, func(tx durable.Tx) (made, error) {
			p, err := durable.CreateTask(tx, retentionRejected(fixtures, &into, rejections), rejectedInput{}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return made{}, err
			}
			b, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "b"}, durable.TaskOptions{Ownership: scaleOwnedBy(p)})
			if err != nil {
				return made{}, err
			}
			c, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: b}})
			return made{p, b, c.Id}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		into.Store(int64(tree.c))
		fixtures.open("b", "completed")
		ownExpectOutcome(t, harness, tree.b, durable.OutcomeCompleted)
		baseline := retentionSizes(harness)
		fixtures.open("rejected", "completed")
		select {
		case err := <-rejections:
			if err == nil {
				t.Fatal("P's terminal commit that creates its own child was accepted, want it rejected")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("P's terminal commit did not return")
		}
		// The rejected commit published nothing; the sweep job still cleans up what its chain load read.
		ownWaitUntil(t, func() bool { return retentionSizes(harness).dropQueue == 0 })
		if got := retentionSizes(harness); got != baseline {
			t.Fatalf("index sizes after the rejected commit %+v, want the baseline %+v", got, baseline)
		}
		mustClose(t, harness)
	})

	t.Run("drops what a read loaded after reopen, without a later publication", func(t *testing.T) {
		fixtures := newOwnFixtures()
		store := storage.NewMemoryStorage()
		opened := ownOpenHarness(t, fixtures, store)
		// A live task D in conversation C, owned by ended B: after reopen D's chain is not loaded.
		type made struct{ b, d durable.TaskId }
		tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
			b, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "b"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return made{}, err
			}
			conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: b}})
			if err != nil {
				return made{}, err
			}
			d, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "d"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &conversation.Id})
			return made{b, d}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("b", "completed")
		ownWaitUntil(t, func() bool { return tkTaskRecord(t, opened.harness, tree.b).State.Status == durable.TaskCompleting })
		mustClose(t, opened.harness)
		store.Reopen()
		fixtures = newOwnFixtures()
		reopened := ownOpenHarness(t, fixtures, store)
		if _, err := reopened.harness.Inspect(testContext); err != nil {
			t.Fatal(err)
		}
		// D is still live, so its chain stays; once D ends, everything below the root goes, published or not.
		if got := retentionSizes(reopened.harness).unloaded; got != 0 {
			t.Fatalf("unloaded after the inspection = %d, want 0", got)
		}
		fixtures.open("d", "completed")
		ownExpectOutcome(t, reopened.harness, tree.d, durable.OutcomeCompleted)
		ownExpectOutcome(t, reopened.harness, tree.b, durable.OutcomeCompleted)
		ownWaitUntil(t, func() bool { return retentionSizes(reopened.harness).dropQueue == 0 })
		if got := retentionSizes(reopened.harness); got.settled != 0 || got.below != 0 || got.unloaded != 0 {
			t.Fatalf("index sizes after the work ended %+v, want settled, below and unloaded 0", got)
		}
		mustClose(t, reopened.harness)
	})

	t.Run("forgets a 20,000-deep chain of ended tasks without recursion", func(t *testing.T) {
		const depth = 20_000
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		baseline := retentionSizes(opened.harness)
		type chain struct{ top, leaf durable.TaskId }
		made, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (chain, error) {
			var top durable.TaskId
			owner := durable.TaskId(0)
			for n := range depth {
				options := durable.TaskOptions{Ownership: conversationOwned}
				if n > 0 {
					options = durable.TaskOptions{Ownership: scaleOwnedBy(owner)}
				}
				id, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: fmt.Sprintf("link%d", n)}, options)
				if err != nil {
					return chain{}, err
				}
				if n == 0 {
					top = id
				}
				owner = id
			}
			leaf, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "leaf"}, durable.TaskOptions{Ownership: scaleOwnedBy(owner)})
			return chain{top, leaf}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		for n := range depth {
			fixtures.open(fmt.Sprintf("link%d", n), "completed")
		}
		fixtures.open("leaf", "completed")
		ownExpectOutcome(t, opened.harness, made.leaf, durable.OutcomeCompleted)
		ownExpectOutcome(t, opened.harness, made.top, durable.OutcomeCompleted)
		scaleWaitUntil(t, func() bool { return retentionSizes(opened.harness) == baseline })
		mustClose(t, opened.harness)
	})

	t.Run("keeps no more after all work ended than a fresh reopen", func(t *testing.T) {
		fixtures := newOwnFixtures()
		store := storage.NewMemoryStorage()
		opened := ownOpenHarness(t, fixtures, store)
		parent, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "parent"}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		for n := range 20 {
			retentionCycle(t, fixtures, opened, parent, n)
		}
		fixtures.open("parent", "completed")
		ownExpectOutcome(t, opened.harness, parent, durable.OutcomeCompleted)
		if err := opened.harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		ownWaitUntil(t, func() bool { return retentionSizes(opened.harness).dropQueue == 0 })
		ended := retentionSizes(opened.harness)
		mustClose(t, opened.harness)
		store.Reopen()
		reopened := ownOpenHarness(t, newOwnFixtures(), store)
		if err := reopened.harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if got := retentionSizes(reopened.harness); got != ended {
			t.Fatalf("a reopened Harness holds %+v, one that ran the work holds %+v", got, ended)
		}
		mustClose(t, reopened.harness)
	})

	t.Run("does not scan queued submissions for an input when nothing cancels", func(t *testing.T) {
		fixtures := newOwnFixtures()
		store := &scanCounting{Storage: storage.NewMemoryStorage()}
		opened := ownOpenHarness(t, fixtures, store)
		busy := ownCreateIn(t, fixtures, opened.root, "busy", false)
		ownBusy(t, opened.root, busy)
		before := store.queued.Load()
		ownSubmit(t, opened.root, ownInput("queued"))
		// Let any reconcile it would cause run.
		time.Sleep(20 * time.Millisecond)
		if got := store.queued.Load(); got != before {
			t.Fatalf("%d scans of queued submissions after an input queued with nothing cancelling, want none", got-before)
		}
		fixtures.open("busy", "completed")
		mustClose(t, opened.harness)
	})

	// Go-only: the same input queued below a forgotten conversation, whose chain the submission loads.
	t.Run("does not scan queued submissions for an input below a forgotten conversation when nothing cancels", func(t *testing.T) {
		fixtures := newOwnFixtures()
		store := &scanCounting{Storage: storage.NewMemoryStorage()}
		opened := ownOpenHarness(t, fixtures, store)
		// Live p owns the ended task b, which owns conversation C: forgotten. A live child of p keeps C busy.
		type made struct {
			p, b, keep   durable.TaskId
			conversation durable.ConversationId
		}
		tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
			p, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "p"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return made{}, err
			}
			b, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "b"}, durable.TaskOptions{Ownership: scaleOwnedBy(p)})
			if err != nil {
				return made{}, err
			}
			conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: b}})
			if err != nil {
				return made{}, err
			}
			keep, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "keep"}, durable.TaskOptions{Ownership: scaleOwnedBy(p)})
			if err != nil {
				return made{}, err
			}
			live, err := docDraft(tx, LiveDoc, conversation.Id)
			if err != nil {
				return made{}, err
			}
			return made{p, b, keep, conversation.Id}, setJSON(live, "run", map[string]any{"taskId": int64(keep), "inputs": []any{}})
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("b", "completed")
		ownExpectOutcome(t, opened.harness, tree.b, durable.OutcomeCompleted)
		ownWaitUntil(t, func() bool { return retentionSizes(opened.harness).dropQueue == 0 })
		before := store.queued.Load()
		if _, err := ownConversation(t, opened.harness, tree.conversation).Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("queued")}); err != nil {
			t.Fatal(err)
		}
		// Let any reconcile it would cause run.
		time.Sleep(20 * time.Millisecond)
		if got := store.queued.Load(); got != before {
			t.Fatalf("%d scans of queued submissions after an input queued with nothing cancelling, want none", got-before)
		}
		fixtures.open("keep", "completed")
		fixtures.open("p", "completed")
		ownExpectOutcome(t, opened.harness, tree.p, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	// A wait on an ended task closes no cycle (#10411), also when the ended task owns the waiter's conversation and the
	// scheduler forgot it: the wait resumes at once.
	t.Run("lets a task wait on a forgotten ended owner", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		type made struct {
			b            durable.TaskId
			conversation durable.ConversationId
		}
		tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
			b, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "b"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return made{}, err
			}
			conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: b}})
			return made{b, conversation.Id}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("b", "completed")
		ownExpectOutcome(t, opened.harness, tree.b, durable.OutcomeCompleted)
		inner := ownConversation(t, opened.harness, tree.conversation)
		waiter, err := durable.Commit(testContext, inner, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, ownWaiter(), waiterInput{On: []durable.TaskId{tree.b}}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		outcome := tkWaitOutcome(t, opened.harness, waiter)
		if outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome %+v, want completed", outcome)
		}
		mustClose(t, opened.harness)
	})
}

// scanCounting counts the scans of queued submissions its Storage serves.
type scanCounting struct {
	durable.Storage
	queued atomic.Int64
}

func (store *scanCounting) ScanSubmissions(ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	if query.Status != nil && *query.Status == durable.SubmissionQueued {
		store.queued.Add(1)
	}
	return store.Storage.ScanSubmissions(ctx, query, limit, cursor)
}

// readCounting counts the task and conversation reads a storage serves (harness-scheduler-retention.test.ts countReads).
type readCounting struct {
	durable.Storage
	reads atomic.Int64
}

func (store *readCounting) Task(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	store.reads.Add(1)
	return store.Storage.Task(ctx, id)
}

func (store *readCounting) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	store.reads.Add(1)
	return store.Storage.Conversation(ctx, id)
}
