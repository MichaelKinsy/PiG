// Ports packages/durable/test/harness-scheduler-indexes.test.ts (Pi 1.2.0): cases where a wrong index changes behaviour,
// each against what the committed records imply (spec §5.4, §5.5). The other cases of the file are
// TestACascadeTakesItsReasonFromTheNearestCancellingOwner (harness_cancellation_barrier_test.go) and TestOwnedTasks*
// (scheduler_owned_tasks_test.go).

package harness

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// commitCounter records the commit, by count, that wrote each terminal task record.
type commitCounter struct {
	durable.Storage
	mu      sync.Mutex
	commits int
	endedIn map[durable.TaskId]int
}

func (store *commitCounter) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	store.mu.Lock()
	store.commits++
	for _, write := range writes {
		if task, ok := write.(durable.TaskWrite); ok && task.Value.State.Status == durable.TaskTerminal {
			store.endedIn[task.Value.Id] = store.commits
		}
	}
	store.mu.Unlock()
	return store.Storage.Commit(ctx, writes)
}

func (store *commitCounter) ended(id durable.TaskId) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.endedIn[id]
}

// conversationRead holds the read of one conversation record until released.
type conversationRead struct {
	durable.Storage
	target  durable.ConversationId
	release chan struct{}
}

func (store *conversationRead) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	if id == store.target {
		select {
		case <-store.release:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	return store.Storage.Conversation(ctx, id)
}

func TestSchedulerIndexes(t *testing.T) {
	// harness-scheduler-indexes.test.ts:125 it("finalizes a chain of held owners in the commit that frees the last one")
	t.Run("finalizes a chain of held owners in the commit that frees the last one", func(t *testing.T) {
		fixtures := newOwnFixtures()
		store := &commitCounter{Storage: openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")), endedIn: map[durable.TaskId]int{}}
		opened := ownOpenHarness(t, fixtures, store)
		type chain struct{ top, middle, leaf durable.TaskId }
		ids, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (chain, error) {
			top, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "top"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return chain{}, err
			}
			middle, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "middle"}, durable.TaskOptions{Ownership: scaleOwnedBy(top)})
			if err != nil {
				return chain{}, err
			}
			leaf, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "leaf"}, durable.TaskOptions{Ownership: scaleOwnedBy(middle)})
			return chain{top, middle, leaf}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("top", "completed")
		fixtures.open("middle", "completed")
		eventually(t, func() bool { return tkTaskRecord(t, opened.harness, ids.top).State.Status == durable.TaskCompleting })
		eventually(t, func() bool { return tkTaskRecord(t, opened.harness, ids.middle).State.Status == durable.TaskCompleting })
		fixtures.open("leaf", "completed")
		ownExpectOutcome(t, opened.harness, ids.top, durable.OutcomeCompleted)
		// Both owners end in one finalize commit, after the leaf's.
		if top, middle, leaf := store.ended(ids.top), store.ended(ids.middle), store.ended(ids.leaf); top != middle || middle <= leaf {
			t.Fatalf("terminal records written by commits top %d, middle %d, leaf %d, want the owners in one commit after the leaf's", top, middle, leaf)
		}
		mustClose(t, opened.harness)
	})

	// harness-scheduler-indexes.test.ts:160 it("keeps a scope busy after reopen for work below an ended task whose chain is not loaded yet")
	t.Run("keeps a scope busy after reopen for work below an ended task whose chain is not loaded yet", func(t *testing.T) {
		fixtures := newOwnFixtures()
		path := filepath.Join(t.TempDir(), "session.sqlite")
		opened := ownOpenHarness(t, fixtures, openSqlite(t, path))
		// root conversation -> owner (ends) -> conversation C -> inner (created after owner ended), as a subagent call's.
		type made struct {
			owner durable.TaskId
			child durable.ConversationId
		}
		tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
			owner, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "owner"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return made{}, err
			}
			child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
			return made{owner, child.Id}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("owner", "completed")
		ownExpectOutcome(t, opened.harness, tree.owner, durable.OutcomeCompleted)
		conversation := ownConversation(t, opened.harness, tree.child)
		inner, err := durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "inner"}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)
		// Hold the read of C's record, so the chain above `inner` stays unloaded while idle is decided.
		reopened := newOwnFixtures()
		held := &conversationRead{Storage: openSqlite(t, path), target: tree.child, release: make(chan struct{})}
		harness, _, _ := openTasks(t, held, []durable.AnyTask{reopened.hold.Erased()})
		// A task whose chain is not loaded counts as inside every scope. (The held read blocks the Session line, so the root
		// handle comes after.)
		idle := goCall(func() error { return harness.WaitForIdle(testContext) })
		if idle.settled() {
			t.Fatal("WaitForIdle returned while the chain above the task was not loaded")
		}
		close(held.release)
		root := mustRoot(t, harness, nil)
		harness.Resume()
		reopened.open("inner", "completed")
		ownExpectOutcome(t, harness, inner, durable.OutcomeCompleted)
		if err := root.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if err := idle.wait(t); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)
	})
}
