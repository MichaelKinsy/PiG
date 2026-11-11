// The scopes above a task are what the idle check of kept contexts visits after the task parks, wakes or ends
// (scheduler.ts #addScopesAbove). Pi 473515f9 makes a park count, so a chain of n tasks that park one commit at a time asks
// n times: the walk must cost the conversations on the way up, not the depth of the chain (Pi 628d8ab52: it starts at the
// task's own conversation, which a child task shares with its owner).

package harness

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// scopeChain is two chains of held tasks joined by a conversation: tasks a1..a<depth> from the root conversation, a
// conversation the last of them owns, and tasks b1..b<depth> in it.
type scopeChain struct {
	scheduler *TaskScheduler
	close     func()
	below     durable.TaskId
	owned     durable.ConversationId
}

func openScopeChain(t *testing.T, depth int) scopeChain {
	t.Helper()
	fixtures := newOwnFixtures()
	opened := ownOpenHarness(t, fixtures)
	type made struct {
		below        durable.TaskId
		conversation durable.ConversationId
	}
	tree, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (made, error) {
		var owner durable.TaskId
		for n := range depth {
			options := durable.TaskOptions{Ownership: conversationOwned}
			if n > 0 {
				options = durable.TaskOptions{Ownership: scaleOwnedBy(owner)}
			}
			id, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: fmt.Sprintf("a%d", n)}, options)
			if err != nil {
				return made{}, err
			}
			owner = id
		}
		conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
		if err != nil {
			return made{}, err
		}
		var below durable.TaskId
		for n := range depth {
			options := durable.TaskOptions{Ownership: conversationOwned, ConversationId: &conversation.Id}
			if n > 0 {
				options = durable.TaskOptions{Ownership: scaleOwnedBy(below)}
			}
			below, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: fmt.Sprintf("b%d", n)}, options)
			if err != nil {
				return made{}, err
			}
		}
		return made{below, conversation.Id}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return scopeChain{
		scheduler: opened.harness.(*harnessImpl).tasks,
		close:     func() { mustClose(t, opened.harness) },
		below:     tree.below,
		owned:     tree.conversation,
	}
}

// aboveScopes is the conversations a plain walk up from the owner of record passes, as a set.
func aboveScopes(s *TaskScheduler, record anyTaskRecord) (map[durable.ConversationId]struct{}, bool) {
	scopes := map[durable.ConversationId]struct{}{}
	for _, each := range s.aboveLocked(parentOf(nodeOf(record)), nil) {
		switch each.kind {
		case stepOwnerUnknown:
			return nil, false
		case stepOwnerConversation:
			scopes[each.conversation] = struct{}{}
		}
	}
	return scopes, true
}

func TestAddScopesAboveMatchesAWalkUp(t *testing.T) {
	chain := openScopeChain(t, 50)
	s := chain.scheduler
	s.mu.Lock()
	checked := 0
	for id, record := range s.live {
		want, known := aboveScopes(s, record)
		if !known {
			t.Fatalf("task %d: a plain walk up does not know its chain", id)
		}
		got := map[durable.ConversationId]struct{}{}
		if !s.addScopesAboveLocked(record, got) {
			t.Fatalf("task %d: addScopesAboveLocked does not know its chain", id)
		}
		if len(got) != len(want) {
			t.Fatalf("task %d: scopes %v, a walk up passes %v", id, got, want)
		}
		for conversationId := range want {
			if _, ok := got[conversationId]; !ok {
				t.Fatalf("task %d: scopes %v, a walk up passes %v", id, got, want)
			}
		}
		checked++
	}
	deepest := s.live[chain.below]
	scopes := map[durable.ConversationId]struct{}{}
	s.addScopesAboveLocked(deepest, scopes)
	s.mu.Unlock()
	if checked != 100 {
		t.Fatalf("checked %d live tasks, want 100", checked)
	}
	if _, ok := scopes[chain.owned]; !ok || len(scopes) != 2 {
		t.Fatalf("the deepest task's scopes %v, want its conversation %d and the root", scopes, chain.owned)
	}
	chain.close()
}

// TestAddScopesAboveCostDoesNotGrowWithTheChain is the gate on the walk: once a chain was walked, the next task's walk takes
// the same few allocations at any depth: the key of each conversation it reads, two here. Walking the owners each time
// allocated a list of the tasks passed that grows with the chain. Gate kept from the memo this walk replaced (5c94d5587).
func TestAddScopesAboveCostDoesNotGrowWithTheChain(t *testing.T) {
	for _, depth := range []int{10, 5_000} {
		t.Run(fmt.Sprintf("depth %d", depth), func(t *testing.T) {
			chain := openScopeChain(t, depth)
			s := chain.scheduler
			s.mu.Lock()
			record := s.live[chain.below]
			scopes := map[durable.ConversationId]struct{}{}
			allocs := testing.AllocsPerRun(20, func() {
				clear(scopes)
				if !s.addScopesAboveLocked(record, scopes) {
					panic("chain not known")
				}
			})
			s.mu.Unlock()
			if allocs > 4 {
				t.Fatalf("a walk up a chain of %d owners allocated %v times, want at most 4 (two conversations)", depth, allocs)
			}
			chain.close()
		})
	}
}
