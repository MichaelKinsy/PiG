// Ports the cases of packages/durable/test/harness-scheduler-retention.test.ts (upstream ea448f45) that work below a forgotten
// conversation: an owner holds for work its own terminal commit creates there, abort intent reaches input and work that
// arrive there, a scope stays busy while their chain loads, and a commit that ends the last task below a chain and starts
// new work keeps the chain.

package harness

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type spawnerInput struct {
	Name string `json:"name"`
}

// retentionSpawner holds until the gate named by its input opens, then creates a held task "inside.<name>" in conversation
// *into and completes in the same commit. Upstream passes the conversation through the task input, set after creation with a
// raw setTask; here the test sets *into before it opens the gate.
func retentionSpawner(fixtures *ownFixtures, into *atomic.Int64) durable.Task[spawnerInput, stepState, durable.JsonValue, any] {
	type record = durable.RunningTask[spawnerInput, stepState, durable.JsonValue]
	type runtime = durable.TaskRuntime[spawnerInput, stepState, durable.JsonValue, any]
	return durable.DefineTask(durable.TaskDefinition[spawnerInput, stepState, durable.JsonValue, any]{
		Name:    "test.retention.spawner",
		Version: 1,
		Initial: func(spawnerInput) stepState { return stepState{Phase: "hold"} },
		Phases: map[string]durable.PhaseHandler[spawnerInput, stepState, durable.JsonValue, any]{
			"hold": func(ctx context.Context, task record, runtime runtime) error {
				select {
				case <-fixtures.gate(task.Input.Name).done:
				case <-runtime.Signal().Done():
					return context.Cause(runtime.Signal())
				}
				return runtime.Commit(ctx, func(tx durable.Tx, _ record) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					conversation := durable.ConversationId(into.Load())
					if _, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "inside." + task.Input.Name}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &conversation}); err != nil {
						return nil, err
					}
					return completed[stepState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ record, runtime runtime) error {
			return runtime.Commit(ctx, func(durable.Tx, record) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue](""), nil
			})
		},
	})
}

// chainRead holds the read of one conversation record, from its nth read once armed, until released.
type chainRead struct {
	durable.Storage
	target  durable.ConversationId
	armed   atomic.Bool
	reads   atomic.Int64
	nth     int64
	reached chan struct{}
	release chan struct{}
}

func (store *chainRead) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	if store.armed.Load() && id == store.target && store.reads.Add(1) == store.nth {
		close(store.reached)
		select {
		case <-store.release:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	return store.Storage.Conversation(ctx, id)
}

// retentionRawTask rewrites a task record through the Transaction, as upstream's raw setTask.
func retentionRawTask(t *testing.T, conversation Conversation, id durable.TaskId, change func(*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue])) {
	t.Helper()
	if _, err := conversation.Commit(testContext, func(tx durable.Tx) (any, error) {
		record, err := tx.Task(id)
		if err != nil || record == nil {
			return nil, err
		}
		changed := *record
		change(&changed)
		return nil, tx.(*session.Transaction).SetTask(changed)
	}); err != nil {
		t.Fatal(err)
	}
}

// retentionForgotten creates live P -> ended B [-> ended A] -> conversation C (no task) and a live child "keep" of P that
// keeps P holding. It ends A and B and returns the ids once B has ended.
type forgottenTree struct {
	p, b, keep   durable.TaskId
	conversation durable.ConversationId
}

func retentionForgotten(t *testing.T, fixtures *ownFixtures, opened ownOpened, twoLevels bool) forgottenTree {
	t.Helper()
	tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (forgottenTree, error) {
		var tree forgottenTree
		var err error
		if tree.p, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "p"}, durable.TaskOptions{Ownership: conversationOwned}); err != nil {
			return tree, err
		}
		if tree.b, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "b"}, durable.TaskOptions{Ownership: scaleOwnedBy(tree.p)}); err != nil {
			return tree, err
		}
		owner := tree.b
		if twoLevels {
			if owner, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "a"}, durable.TaskOptions{Ownership: scaleOwnedBy(tree.b)}); err != nil {
				return tree, err
			}
		}
		conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
		if err != nil {
			return tree, err
		}
		tree.conversation = conversation.Id
		tree.keep, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "keep"}, durable.TaskOptions{Ownership: scaleOwnedBy(tree.p)})
		return tree, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if twoLevels {
		fixtures.open("a", "completed")
	}
	fixtures.open("b", "completed")
	ownExpectOutcome(t, opened.harness, tree.b, durable.OutcomeCompleted)
	return tree
}

func TestSchedulerRetentionBelowAForgottenConversation(t *testing.T) {
	t.Run("holds an owner for work its own terminal commit creates below a forgotten chain", func(t *testing.T) {
		fixtures := newOwnFixtures()
		var into atomic.Int64
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{fixtures.hold.Erased(), retentionSpawner(fixtures, &into).Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		// Live P (a spawner) -> ended B -> ended A -> conversation C, empty: forgotten. P's terminal commit creates D in C.
		type made struct {
			p, b durable.TaskId
			c    durable.ConversationId
		}
		tree, err := durable.Commit(testContext, root, func(tx durable.Tx) (made, error) {
			p, err := durable.CreateTask(tx, retentionSpawner(fixtures, &into), spawnerInput{Name: "p"}, durable.TaskOptions{Ownership: conversationOwned})
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
			c, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: a}})
			return made{p, b, c.Id}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		into.Store(int64(tree.c))
		fixtures.open("a", "completed")
		fixtures.open("b", "completed")
		ownExpectOutcome(t, harness, tree.b, durable.OutcomeCompleted)
		fixtures.open("p", "completed")
		ownWaitUntil(t, func() bool { return ownStatus(t, harness, tree.p).Status != durable.TaskRunning })
		if got := ownStatus(t, harness, tree.p).Status; got != durable.TaskCompleting {
			t.Fatalf("P is %s, want completing while the work its commit created below the forgotten chain lives", got)
		}
		fixtures.open("inside.p", "completed")
		ownExpectOutcome(t, harness, tree.p, durable.OutcomeCompleted)
		mustClose(t, harness)
	})

	t.Run("withdraws an input queued in a forgotten conversation below an abort-marked owner", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := retentionForgotten(t, fixtures, opened, false)
		child := ownConversation(t, opened.harness, tree.conversation)
		// Busy, so the input queues instead of starting a run.
		ownBusy(t, child, tree.keep)
		queued := ownSubmit(t, child, ownInput("later"))
		if _, err := opened.harness.AbortTask(testContext, tree.p); err != nil {
			t.Fatal(err)
		}
		settled, err := queued.Wait(testContext)
		if err != nil {
			t.Fatal(err)
		}
		if settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("queued input %+v, want unanswered: aborted", settled)
		}
		tkWaitOutcome(t, opened.harness, tree.p)
		mustClose(t, opened.harness)
	})

	t.Run("aborts work created in a forgotten conversation below an abort-marked owner before it runs", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := retentionForgotten(t, fixtures, opened, false)
		// P cancels; its live child keeps it holding.
		retentionRawTask(t, opened.root, tree.p, func(record *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) {
			record.AbortRequested = true
		})
		ownWaitUntil(t, func() bool { return tkTaskRecord(t, opened.harness, tree.keep).AbortRequested })
		child := ownConversation(t, opened.harness, tree.conversation)
		late, err := durable.Commit(testContext, child, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "late"}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		ownExpectOutcome(t, opened.harness, late, durable.OutcomeAborted)
		if runs := fixtures.runCount("late"); runs != 0 {
			t.Fatalf("late ran %d times, want 0", runs)
		}
		mustClose(t, opened.harness)
	})

	t.Run("keeps a scope busy while the chain of new work below a forgotten conversation loads", func(t *testing.T) {
		fixtures := newOwnFixtures()
		store := &chainRead{Storage: storage.NewMemoryStorage(), nth: 2, reached: make(chan struct{}), release: make(chan struct{})}
		opened := ownOpenHarness(t, fixtures, store)
		type made struct {
			b            durable.TaskId
			conversation durable.ConversationId
		}
		tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
			b, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "b"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return made{}, err
			}
			c, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: b}})
			return made{b, c.Id}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("b", "completed")
		ownExpectOutcome(t, opened.harness, tree.b, durable.OutcomeCompleted)
		// Hold the chain load's read of C: the second read once holding, after the creating commit's own.
		store.target = tree.conversation
		child := ownConversation(t, opened.harness, tree.conversation)
		store.armed.Store(true)
		if _, err := durable.Commit(testContext, child, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "d"}, durable.TaskOptions{Ownership: conversationOwned})
		}); err != nil {
			t.Fatal(err)
		}
		<-store.reached
		idle := goCall(func() error { return opened.harness.WaitForIdle(testContext) })
		if idle.settled() {
			t.Fatal("WaitForIdle returned while the chain of the new work was loading")
		}
		close(store.release)
		fixtures.open("d", "completed")
		if err := idle.wait(t); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)
	})

	t.Run("keeps a chain when one commit ends its last task and creates new work below it", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		// Live P -> ended B -> conversation C with background task `old`, which keeps the chain known without holding B.
		type made struct {
			p, b, old    durable.TaskId
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
			c, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: b}})
			if err != nil {
				return made{}, err
			}
			old, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "old"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &c.Id, Background: true})
			return made{p, b, old, c.Id}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("b", "completed")
		ownExpectOutcome(t, opened.harness, tree.b, durable.OutcomeCompleted)
		// One commit: the old task ends, which leaves nothing live below B, and a replacement starts in C.
		replacement, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			old, err := tx.Task(tree.old)
			if err != nil || old == nil {
				return 0, err
			}
			id, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "new"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &tree.conversation})
			if err != nil {
				return 0, err
			}
			ended := *old
			ended.State = anyTaskState{Status: durable.TaskTerminal, Outcome: &anyOutcome{Status: durable.OutcomeCompleted}}
			return id, tx.(*session.Transaction).SetTask(ended)
		})
		if err != nil {
			t.Fatal(err)
		}
		// The chain stayed known through the publication, so P holds for the replacement.
		fixtures.open("p", "completed")
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, tree.p).Status == durable.TaskCompleting })
		fixtures.open("new", "completed")
		ownExpectOutcome(t, opened.harness, replacement, durable.OutcomeCompleted)
		ownExpectOutcome(t, opened.harness, tree.p, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	t.Run("withdraws an input queued two ended levels below an owner whose intent appears later", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		// Live P -> ended B -> ended A -> conversation C with no task, and a live child keeping P holding.
		tree := retentionForgotten(t, fixtures, opened, true)
		child := ownConversation(t, opened.harness, tree.conversation)
		ownBusy(t, child, tree.keep)
		// Queued while nothing cancels: no withdrawal scan.
		queued := ownSubmit(t, child, ownInput("later"))
		ownWaitUntil(t, func() bool { return retentionSizes(opened.harness).dropQueue == 0 })
		// P fails while its child runs: it holds the failed outcome, which is cancellation intent.
		retentionRawTask(t, opened.root, tree.p, func(record *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) {
			record.State = anyTaskState{Status: durable.TaskCompleting, Outcome: &anyOutcome{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: "x"}}}
		})
		settled, err := queued.Wait(testContext)
		if err != nil {
			t.Fatal(err)
		}
		if settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("queued input %+v, want unanswered: aborted", settled)
		}
		mustClose(t, opened.harness)
	})
}
