// Ports the Pi 1.2.0 cases of packages/durable/test/harness-ownership.test.ts that keep work below ended tasks owned by the
// live task above them, reject a conversation created in its owner's finishing commit, and fail the Harness on a failed
// cascade commit. Go's harness_ownership_test.go helpers (Hold, ownedChild, gates) are upstream's.

package harness

// pi: packages/durable/src/harness/scheduler.ts

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// ownSpawner tries to create, in the commit that ends it, a conversation it owns with a held task in it, named by its
// input (harness-ownership.test.ts Spawner).
func ownSpawner(fixtures *ownFixtures) durable.Task[holdInput, stepState, durable.JsonValue, any] {
	type record = durable.RunningTask[holdInput, stepState, durable.JsonValue]
	type runtime = durable.TaskRuntime[holdInput, stepState, durable.JsonValue, any]
	return durable.DefineTask(durable.TaskDefinition[holdInput, stepState, durable.JsonValue, any]{
		Name:    "test.spawner",
		Version: 1,
		Initial: func(holdInput) stepState { return stepState{Phase: "spawn"} },
		Phases: map[string]durable.PhaseHandler[holdInput, stepState, durable.JsonValue, any]{
			"spawn": func(ctx context.Context, task record, runtime runtime) error {
				return runtime.Commit(ctx, func(tx durable.Tx, _ record) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: task.Id}})
					if err != nil {
						return nil, err
					}
					if _, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: task.Input.Name}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &child.Id}); err != nil {
						return nil, err
					}
					return completed[stepState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ record, runtime runtime) error {
			return runtime.Commit(ctx, func(durable.Tx, record) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
}

// ownCreateIn commits a held task named name in conversation.
func ownCreateIn(t *testing.T, fixtures *ownFixtures, conversation Conversation, name string, slowAbort bool) durable.TaskId {
	t.Helper()
	id, err := durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, fixtures.hold, holdInput{Name: name, SlowAbort: slowAbort}, durable.TaskOptions{Ownership: conversationOwned})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// ownEndedChain is live P owning B, which owns A, which owns conversation C; with background, C holds the background
// task Z, else C is empty.
type ownEndedChain struct {
	p, b, a, z durable.TaskId
	c          durable.ConversationId
}

func ownStageEndedChain(t *testing.T, fixtures *ownFixtures, root Conversation, background bool) ownEndedChain {
	t.Helper()
	tree, err := durable.Commit(testContext, root, func(tx durable.Tx) (ownEndedChain, error) {
		p, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "p"}, durable.TaskOptions{Ownership: conversationOwned})
		if err != nil {
			return ownEndedChain{}, err
		}
		b, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "b"}, durable.TaskOptions{Ownership: scaleOwnedBy(p)})
		if err != nil {
			return ownEndedChain{}, err
		}
		a, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "a"}, durable.TaskOptions{Ownership: scaleOwnedBy(b)})
		if err != nil {
			return ownEndedChain{}, err
		}
		c, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: a}})
		if err != nil || !background {
			return ownEndedChain{p: p, b: b, a: a, c: c.Id}, err
		}
		z, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "z"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &c.Id, Background: true})
		return ownEndedChain{p: p, b: b, a: a, z: z, c: c.Id}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// ownBridge is live P (slow abort) owning depth ended tasks in a chain, the last owning conversation C, empty when they
// ended (harness-ownership.test.ts endedBridge).
type ownBridge struct {
	opened ownOpened
	p      durable.TaskId
	c      Conversation
}

func ownEndedBridge(t *testing.T, fixtures *ownFixtures, depth int) ownBridge {
	t.Helper()
	opened := ownOpenHarness(t, fixtures)
	type made struct {
		p     durable.TaskId
		links []durable.TaskId
		c     durable.ConversationId
	}
	tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
		p, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "p", SlowAbort: true}, durable.TaskOptions{Ownership: conversationOwned})
		if err != nil {
			return made{}, err
		}
		links := []durable.TaskId{}
		owner := p
		for n := range depth {
			if owner, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: fmt.Sprintf("link%d", n)}, durable.TaskOptions{Ownership: scaleOwnedBy(owner)}); err != nil {
				return made{}, err
			}
			links = append(links, owner)
		}
		c, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
		return made{p: p, links: links, c: c.Id}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	for n := depth - 1; n >= 0; n-- {
		fixtures.open(fmt.Sprintf("link%d", n), "completed")
	}
	for _, id := range tree.links {
		tkWaitOutcome(t, opened.harness, id)
	}
	return ownBridge{opened: opened, p: tree.p, c: ownConversation(t, opened.harness, tree.c)}
}

// ownMarkFailingStorage fails the commit that writes the abort mark of the task in failMark, once.
type ownMarkFailingStorage struct {
	durable.Storage
	mu       sync.Mutex
	failMark durable.TaskId
	failure  error
}

func (store *ownMarkFailingStorage) fail(id durable.TaskId) {
	store.mu.Lock()
	store.failMark = id
	store.mu.Unlock()
}

func (store *ownMarkFailingStorage) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	store.mu.Lock()
	marks := false
	for _, write := range writes {
		if task, ok := write.(durable.TaskWrite); ok && store.failMark != 0 && task.Value.Id == store.failMark && task.Value.AbortRequested {
			marks = true
		}
	}
	if marks {
		store.failMark = 0
	}
	store.mu.Unlock()
	if marks {
		return 0, store.failure
	}
	return store.Storage.Commit(ctx, writes, adopt)
}

func ownExpectFailedEnd(t *testing.T, harness Harness, failure error) {
	t.Helper()
	select {
	case <-harness.Closed():
	case <-time.After(10 * time.Second):
		t.Fatal("the Harness did not fail")
	}
	if end := harness.End(); end.Reason != durable.SessionEndFailed || (failure != nil && !errors.Is(end.Error, failure)) {
		t.Fatalf("end %+v, want failed with %v", end, failure)
	}
}

func TestOwnershipBelowEndedTasks(t *testing.T) {
	// harness-ownership.test.ts "keeps ownership queries off finished subtrees". Upstream counts Set iterations while the
	// owner settles by patching Set.prototype[Symbol.iterator]; Go has no hook into its maps' iteration, so the count
	// (entries < chains) is not asserted. The 50 ended chains below the live owner, each ending in an empty owned
	// conversation, and the owner's completion are.
	t.Run("keeps ownership queries off finished subtrees", func(t *testing.T) {
		const chains = 50
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		owner := ownCreateIn(t, fixtures, opened.root, "owner", false)
		tasks := []durable.TaskId{}
		for n := range chains {
			ids, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) ([]durable.TaskId, error) {
				first, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: fmt.Sprintf("c%d.0", n)}, durable.TaskOptions{Ownership: scaleOwnedBy(owner)})
				if err != nil {
					return nil, err
				}
				second, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: fmt.Sprintf("c%d.1", n)}, durable.TaskOptions{Ownership: scaleOwnedBy(first)})
				if err != nil {
					return nil, err
				}
				_, err = tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: second}})
				return []durable.TaskId{first, second}, err
			})
			if err != nil {
				t.Fatal(err)
			}
			tasks = append(tasks, ids...)
		}
		for n := range chains {
			fixtures.open(fmt.Sprintf("c%d.1", n), "completed")
			fixtures.open(fmt.Sprintf("c%d.0", n), "completed")
		}
		for _, id := range tasks {
			tkWaitOutcome(t, opened.harness, id)
		}
		fixtures.open("owner", "completed")
		ownExpectOutcome(t, opened.harness, owner, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	t.Run("holds an owner for work a commit creates below ended tasks loaded from storage", func(t *testing.T) {
		path := sqlitePath(t)
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures, tkOpenSqlite(t, path))
		tree := ownStageEndedChain(t, fixtures, opened.root, false)
		fixtures.open("a", "completed")
		fixtures.open("b", "completed")
		tkWaitOutcome(t, opened.harness, tree.a)
		tkWaitOutcome(t, opened.harness, tree.b)
		mustClose(t, opened.harness)
		// After reopen, B and A are known only once a chain load reads them.
		opened = ownOpenHarness(t, fixtures, tkOpenSqlite(t, path))
		d := ownCreateIn(t, fixtures, ownConversation(t, opened.harness, tree.c), "d", false)
		fixtures.open("p", "completed")
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, tree.p).Status == durable.TaskCompleting })
		fixtures.open("d", "completed")
		ownExpectOutcome(t, opened.harness, d, durable.OutcomeCompleted)
		ownExpectOutcome(t, opened.harness, tree.p, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	t.Run("holds an owner for work created later below ended tasks, without a reopen", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		tree := ownStageEndedChain(t, fixtures, opened.root, false)
		fixtures.open("a", "completed")
		fixtures.open("b", "completed")
		tkWaitOutcome(t, opened.harness, tree.b)
		d := ownCreateIn(t, fixtures, ownConversation(t, opened.harness, tree.c), "d", false)
		fixtures.open("p", "completed")
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, tree.p).Status == durable.TaskCompleting })
		fixtures.open("d", "completed")
		ownExpectOutcome(t, opened.harness, d, durable.OutcomeCompleted)
		ownExpectOutcome(t, opened.harness, tree.p, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	for _, depth := range []int{2, 5} {
		t.Run(fmt.Sprintf("aborts work below %d ended tasks with their live owner, which waits for it", depth), func(t *testing.T) {
			fixtures := newOwnFixtures()
			bridge := ownEndedBridge(t, fixtures, depth)
			harness := bridge.opened.harness
			d := ownCreateIn(t, fixtures, bridge.c, "d", false)
			if _, err := harness.AbortTask(testContext, bridge.p); err != nil {
				t.Fatal(err)
			}
			ownExpectOutcome(t, harness, d, durable.OutcomeAborted)
			// P's abort handler ran only after D ended.
			fixtures.open("abort.p", "completed")
			ownExpectOutcome(t, harness, bridge.p, durable.OutcomeAborted)
			mustClose(t, harness)
		})

		t.Run(fmt.Sprintf("keeps the root busy and aborts with it work below %d ended tasks", depth), func(t *testing.T) {
			fixtures := newOwnFixtures()
			bridge := ownEndedBridge(t, fixtures, depth)
			harness, root := bridge.opened.harness, bridge.opened.root
			fixtures.open("p", "completed")
			tkWaitOutcome(t, harness, bridge.p)
			// P has ended: D and D2 hang below ended tasks only, and are still the root conversation's work.
			d := ownCreateIn(t, fixtures, bridge.c, "d", false)
			d2 := ownCreateIn(t, fixtures, bridge.c, "d2", false)
			if goCall(func() error { return root.WaitForIdle(testContext) }).settled() {
				t.Fatal("the root is idle while work below ended tasks is live")
			}
			if goCall(func() error { return harness.WaitForIdle(testContext) }).settled() {
				t.Fatal("the Harness is idle while work below ended tasks is live")
			}
			if err := root.Abort(testContext, nil); err != nil {
				t.Fatal(err)
			}
			for _, id := range []durable.TaskId{d, d2} {
				ownExpectOutcome(t, harness, id, durable.OutcomeAborted)
			}
			mustClose(t, harness)
		})
	}

	// Live P -> ended B -> conversation C -> ended X -> ended A -> conversation C2; work starts in C2 later. X owns no
	// conversation, only the task A, so only owning a known task keeps it.
	t.Run("reaches work below ended tasks across two owned conversations", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		type made struct {
			p, b, x durable.TaskId
			c2      durable.ConversationId
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
			x, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "x"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &c.Id})
			if err != nil {
				return made{}, err
			}
			a, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "a"}, durable.TaskOptions{Ownership: scaleOwnedBy(x)})
			if err != nil {
				return made{}, err
			}
			c2, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: a}})
			return made{p: p, b: b, x: x, c2: c2.Id}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("a", "completed")
		fixtures.open("x", "completed")
		fixtures.open("b", "completed")
		tkWaitOutcome(t, opened.harness, tree.b)
		d := ownCreateIn(t, fixtures, ownConversation(t, opened.harness, tree.c2), "d", false)
		fixtures.open("p", "completed")
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, tree.p).Status == durable.TaskCompleting })
		if goCall(func() error { return opened.root.WaitForIdle(testContext) }).settled() {
			t.Fatal("the root is idle while work below ended tasks is live")
		}
		if err := opened.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		ownExpectOutcome(t, opened.harness, d, durable.OutcomeAborted)
		ownExpectOutcome(t, opened.harness, tree.p, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	for _, ending := range []string{"completes", "is aborted"} {
		t.Run("frees an owner that "+ending+" once the last work below ended tasks ends after a background task there", func(t *testing.T) {
			fixtures := newOwnFixtures()
			opened := ownOpenHarness(t, fixtures)
			// Live P owns B, which owns A, which owns conversation C with background Z and, later, foreground D.
			tree := ownStageEndedChain(t, fixtures, opened.root, true)
			fixtures.open("a", "completed")
			fixtures.open("b", "completed")
			tkWaitOutcome(t, opened.harness, tree.b)
			c := ownConversation(t, opened.harness, tree.c)
			d := ownCreateIn(t, fixtures, c, "d", true)
			if ending == "completes" {
				fixtures.open("p", "completed")
				ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, tree.p).Status == durable.TaskCompleting })
			} else {
				// P waits for D's abort before its own abort handler runs.
				if _, err := opened.harness.AbortTask(testContext, tree.p); err != nil {
					t.Fatal(err)
				}
				ownWaitUntil(t, func() bool { return tkTaskRecord(t, opened.harness, d).AbortRequested })
			}
			// Z ends first, D last.
			fixtures.open("z", "completed")
			tkWaitOutcome(t, opened.harness, tree.z)
			if ending == "completes" {
				fixtures.open("d", "completed")
			} else {
				fixtures.open("abort.d", "completed")
			}
			tkWaitOutcome(t, opened.harness, d)
			want := durable.OutcomeCompleted
			if ending != "completes" {
				want = durable.OutcomeAborted
			}
			ownExpectOutcome(t, opened.harness, tree.p, want)
			mustClose(t, opened.harness)
		})
	}

	// harness-ownership.test.ts "finalizes many held siblings that end together in one pass". Upstream bounds the Set
	// iterations while it all finalizes (count * 20) by patching Set.prototype[Symbol.iterator]; Go has no hook into its
	// maps' iteration, so that bound is not asserted. The 1000 held siblings, their leaves ended in one raw commit, and the
	// top's completion are.
	t.Run("finalizes many held siblings that end together in one pass", func(t *testing.T) {
		const count = 1000
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		type made struct {
			top    durable.TaskId
			leaves []durable.TaskId
		}
		tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
			top, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "top"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return made{}, err
			}
			siblings := make([]durable.TaskId, count)
			for n := range count {
				if siblings[n], err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: fmt.Sprintf("s%d", n)}, durable.TaskOptions{Ownership: scaleOwnedBy(top)}); err != nil {
					return made{}, err
				}
			}
			// Leaves in reverse sibling order: the last sibling is freed first.
			leaves := []durable.TaskId{}
			for n := count - 1; n >= 0; n-- {
				leaf, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: fmt.Sprintf("l%d", n)}, durable.TaskOptions{Ownership: scaleOwnedBy(siblings[n])})
				if err != nil {
					return made{}, err
				}
				leaves = append(leaves, leaf)
			}
			return made{top: top, leaves: leaves}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for n := range count {
			fixtures.open(fmt.Sprintf("s%d", n), "completed")
		}
		fixtures.open("top", "completed")
		scaleWaitUntil(t, func() bool { return ownStatus(t, opened.harness, tree.top).Status == durable.TaskCompleting })
		if _, err := opened.root.Commit(testContext, func(tx durable.Tx) (any, error) {
			// Reads before the first write.
			records := []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{}
			for _, id := range tree.leaves {
				record, err := tx.Task(id)
				if err != nil {
					return nil, err
				}
				records = append(records, *record)
			}
			internal := tx.(*session.Transaction)
			result := durable.JsonValue(nil)
			for _, record := range records {
				record.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}}
				if err := internal.SetTask(record); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}); err != nil {
			t.Fatal(err)
		}
		ownExpectOutcome(t, opened.harness, tree.top, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	// Pins the Transaction rule that a conversation cannot be created in the commit that ends its owner task.
	t.Run("rejects a conversation created in the commit that ends its owner task", func(t *testing.T) {
		fixtures := newOwnFixtures()
		spawner := ownSpawner(fixtures)
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{fixtures.hold.Erased(), ownWaiter().Erased(), spawner.Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		id, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, spawner, holdInput{Name: "spawned"}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		outcome := tkWaitOutcome(t, harness, id)
		pattern := regexp.MustCompile(fmt.Sprintf(`^Conversation owner task %d is (completing|terminal)$`, id))
		if outcome.Status != durable.OutcomeFaulted || outcome.Error == nil || !pattern.MatchString(outcome.Error.Message) {
			t.Fatalf("outcome %+v, want faulted: Conversation owner task %d is completing|terminal", outcome, id)
		}
		mustClose(t, harness)
	})
}

func TestOwnershipFailedCascadeCommits(t *testing.T) {
	t.Run("applies marks found through an edge loaded after reopen once a failed cascade commit is reopened", func(t *testing.T) {
		path := sqlitePath(t)
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures, tkOpenSqlite(t, path))
		tree := ownedChild(t, fixtures, opened.root, "owner", ownOptions{slowOwner: true})
		fixtures.open("owner.inner", "completed")
		tkWaitOutcome(t, opened.harness, tree.inner)
		if _, err := opened.harness.AbortTask(testContext, tree.owner); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)

		failing := &ownMarkFailingStorage{Storage: tkOpenSqlite(t, path), failure: errors.New("disk gone")}
		opened = ownOpenHarness(t, fixtures, failing)
		late, err := durable.Commit(testContext, ownConversation(t, opened.harness, tree.child), func(tx durable.Tx) (durable.TaskId, error) {
			id, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "late"}, durable.TaskOptions{Ownership: conversationOwned})
			failing.fail(id)
			return id, err
		})
		if err != nil {
			t.Fatal(err)
		}
		// The failed cascade commit fails the Harness. Closed is closed once its invocations have ended, and the owner's
		// abort handler waits for its gate: let it return.
		fixtures.open("abort.owner", "completed")
		ownExpectFailedEnd(t, opened.harness, failing.failure)
		_ = opened.harness.Close(testContext)
		// Reopening derives the mark again.
		opened = ownOpenHarness(t, fixtures, tkOpenSqlite(t, path))
		ownExpectOutcome(t, opened.harness, late, durable.OutcomeAborted)
		mustClose(t, opened.harness)
	})

	t.Run("fails the Harness on a failed cascade commit, and reopening applies the cascade", func(t *testing.T) {
		memory := storage.NewMemoryStorage()
		failing := &ownMarkFailingStorage{Storage: memory, failure: errors.New("disk gone")}
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures, failing)
		tree := ownedChild(t, fixtures, opened.root, "owner")
		failing.fail(tree.inner)
		fixtures.open("owner", "failed")
		ownExpectFailedEnd(t, opened.harness, failing.failure)
		if got := opened.reports.all(); len(got) != 1 || !errors.Is(got[0], failing.failure) {
			t.Fatalf("reports %v, want the storage error once", got)
		}
		_ = opened.harness.Close(testContext)
		memory.Reopen()
		reopened := ownOpenHarness(t, fixtures, memory)
		ownExpectOutcome(t, reopened.harness, tree.inner, durable.OutcomeAborted)
		mustClose(t, reopened.harness)
	})
}
