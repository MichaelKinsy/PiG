// Ports the scaling cases of packages/durable/test/harness-ownership.test.ts added with upstream cb437549: a reconcile
// pass visits the tasks it concerns, not every live task. A chain of n held tasks cost about n^3 steps before.

package harness

import (
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
)

// scaleWaitUntil waits up to a minute, the budget upstream gives its deep-chain case.
func scaleWaitUntil(t *testing.T, check func() bool) {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); !check(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("Condition was not reached")
		}
	}
}

func TestSchedulerScaling(t *testing.T) {
	// 20,000 tasks, each owning the next, created in one commit above a live leaf; they end top-down and hold.
	t.Run("tracks ownership chains deeper than the call stack", func(t *testing.T) {
		const depth = 20_000
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		type chain struct{ top, leaf durable.TaskId }
		made, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (chain, error) {
			top, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "top"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return chain{}, err
			}
			owner := top
			for n := range depth {
				owner, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: fmt.Sprintf("link%d", n)}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: owner}})
				if err != nil {
					return chain{}, err
				}
			}
			leaf, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "leaf"}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: owner}})
			return chain{top, leaf}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		for n := range depth {
			fixtures.open(fmt.Sprintf("link%d", n), "completed")
		}
		fixtures.open("top", "completed")
		scaleWaitUntil(t, func() bool { return ownStatus(t, opened.harness, made.top).Status == durable.TaskCompleting })
		fixtures.open("leaf", "completed")
		ownExpectOutcome(t, opened.harness, made.leaf, durable.OutcomeCompleted)
		ownExpectOutcome(t, opened.harness, made.top, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	// The ended child's conversation still belongs to the owner above it.
	t.Run("holds an owner for work in a conversation its ended child task owns", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		type made struct {
			owner, child durable.TaskId
			conversation durable.ConversationId
		}
		tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
			owner, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "owner"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return made{}, err
			}
			child, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "child"}, durable.TaskOptions{Ownership: scaleOwnedBy(owner)})
			if err != nil {
				return made{}, err
			}
			conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: child}})
			return made{owner, child, conversation.Id}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("child", "completed")
		ownExpectOutcome(t, opened.harness, tree.child, durable.OutcomeCompleted)
		late := ownConversation(t, opened.harness, tree.conversation)
		if _, err := durable.Commit(testContext, late, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "late"}, durable.TaskOptions{Ownership: conversationOwned})
		}); err != nil {
			t.Fatal(err)
		}
		fixtures.open("owner", "completed")
		ownWaitUntil(t, func() bool { return ownStatus(t, opened.harness, tree.owner).Status == durable.TaskCompleting })
		fixtures.open("late", "completed")
		ownExpectOutcome(t, opened.harness, tree.owner, durable.OutcomeCompleted)
		mustClose(t, opened.harness)
	})

	// root conversation -> A -> B -> conversation -> background C -> D
	t.Run("cascades through ended tasks into an owned background subtree when aborting across background", func(t *testing.T) {
		fixtures := newOwnFixtures()
		opened := ownOpenHarness(t, fixtures)
		type made struct{ a, b, c, d durable.TaskId }
		tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
			a, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "a"}, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return made{}, err
			}
			b, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "b"}, durable.TaskOptions{Ownership: scaleOwnedBy(a)})
			if err != nil {
				return made{}, err
			}
			conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: b}})
			if err != nil {
				return made{}, err
			}
			c, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "c"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &conversation.Id, Background: true})
			if err != nil {
				return made{}, err
			}
			d, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "d"}, durable.TaskOptions{Ownership: scaleOwnedBy(c)})
			return made{a, b, c, d}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtures.open("b", "completed")
		fixtures.open("a", "completed")
		ownExpectOutcome(t, opened.harness, tree.a, durable.OutcomeCompleted)
		if err := opened.root.Abort(testContext, &durable.ConversationAbortOptions{Background: true}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []durable.TaskId{tree.c, tree.d} {
			ownExpectOutcome(t, opened.harness, id, durable.OutcomeAborted)
		}
		mustClose(t, opened.harness)
	})
}

func scaleOwnedBy(owner durable.TaskId) durable.TaskOwnership {
	return durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: owner}
}
