// Ports packages/durable/src/harness/scheduler.ts (the idle checks and kept contexts of upstream 35dfa757 and e5c6182b):
// a task change checks only the scopes above it, kept contexts are advanced by the entries of published commits, and the
// idle ones expire in the order they went idle.

package harness

import (
	"math"
	"slices"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// addScopesAboveLocked adds the conversations above record to scopes: those whose idleness a change of it can change. It
// is false when a node on the way up is not known. A conversation already in scopes has everything above it there too, and
// so has a task already walked up from (walked: the tasks of this publication whose chain scopes already holds), so the
// tasks of one commit that hang in a chain are walked once in all, not once each.
func (s *TaskScheduler) addScopesAboveLocked(record anyTaskRecord, scopes map[durable.ConversationId]struct{}, walked map[durable.TaskId]struct{}) bool {
	var passed []durable.TaskId
	at := parentOf(nodeOf(record))
	for {
		if at.isTask {
			if _, seen := walked[at.task]; seen {
				break
			}
			node, ok := s.nodeLocked(at.task, nil)
			if !ok {
				return false
			}
			passed = append(passed, at.task)
			at = parentOf(node)
			continue
		}
		if _, seen := scopes[at.conversation]; seen {
			break
		}
		scopes[at.conversation] = struct{}{}
		edge, known := s.edgeLocked(at.conversation, nil)
		if !known {
			return false
		}
		if edge == nil {
			break
		}
		at = up{isTask: true, task: *edge}
	}
	for _, id := range passed {
		walked[id] = struct{}{}
	}
	return true
}

// advanceContextsLocked extends each kept context range with the entries a stored commit appended to its conversation, so
// a later read needs no Storage scan. Entries are published in ID order.
func (s *TaskScheduler) advanceContextsLocked(publication session.ObservedCommit) {
	if len(s.contexts) == 0 {
		return
	}
	var appended map[durable.ConversationId][]durable.EntryRecord
	var order []durable.ConversationId
	for _, change := range publication.Changes {
		write, ok := change.(durable.EntryWrite)
		if !ok {
			continue
		}
		if _, kept := s.contexts[write.Value.ConversationId]; !kept {
			continue
		}
		if appended == nil {
			appended = map[durable.ConversationId][]durable.EntryRecord{}
		}
		if _, seen := appended[write.Value.ConversationId]; !seen {
			order = append(order, write.Value.ConversationId)
		}
		appended[write.Value.ConversationId] = append(appended[write.Value.ConversationId], write.Value)
	}
	for _, conversationId := range order {
		kept := s.contexts[conversationId]
		// An idle range past its retention is dropped here too: entry-only commits do not settle idleness, and a host may
		// decline the expiry timer. expiresAt was fixed when it went idle, so no settings are read here.
		if kept.idleSince != nil && kept.expiresAt != nil && *kept.expiresAt <= s.options.Now() {
			s.dropContextLocked(conversationId)
			continue
		}
		next, keep := advanceRange(kept.rng, appended[conversationId])
		if !keep {
			s.dropContextLocked(conversationId)
		} else {
			kept.rng = next
		}
	}
}

// settleIdleScopes is the idle check after task changes below the conversations in scopes (every conversation when all):
// it drops the kept contexts whose idle period passed the retention, then starts or ends the idle periods of the kept
// contexts in scopes. A task change can change the idleness only of the conversations above it, so no check visits every
// conversation.
func (s *TaskScheduler) settleIdleScopes(scopes map[durable.ConversationId]struct{}, all bool) {
	now := s.options.Now()
	retention := s.contextRetentionMs()
	// The expiry timer reads the setting again, as its own call does upstream.
	expiryRetention := s.contextRetentionMs()
	s.mu.Lock()
	// Expired first: a context past its retention is dropped even when the same commit makes it busy again.
	s.expireContextsLocked(now, retention)
	var covered []durable.ConversationId
	if all {
		covered = make([]durable.ConversationId, 0, len(s.contexts))
		for conversationId := range s.contexts {
			covered = append(covered, conversationId)
		}
	} else {
		covered = make([]durable.ConversationId, 0, len(scopes))
		for conversationId := range scopes {
			covered = append(covered, conversationId)
		}
	}
	slices.Sort(covered)
	for _, conversationId := range covered {
		kept := s.contexts[conversationId]
		if kept == nil {
			continue
		}
		switch {
		case !s.inactiveLocked(conversationId):
			if kept.idleSince == nil {
				continue
			}
			kept.idleSince, kept.expiresAt = nil, nil
			s.idleOrder.Delete(conversationId)
		case retention <= 0:
			s.dropContextLocked(conversationId)
		case kept.idleSince == nil:
			kept.idleSince, kept.expiresAt = new(now), new(now+retention)
			s.markIdleLocked(conversationId, now)
		}
	}
	s.scheduleExpiryLocked(now, expiryRetention)
	s.mu.Unlock()
}

// idleScopesLocked takes the scopes an idle check covers after a commit's task changes: dirty, or every scope when a chain
// load made a scope idle without a task change below it (unknownsChanged) or a chain of the commit was not known (dirty is
// nil). It counts a full check.
func (s *TaskScheduler) idleScopesLocked(dirty map[durable.ConversationId]struct{}) (scopes map[durable.ConversationId]struct{}, all bool) {
	all = dirty == nil || s.unknownsChanged
	s.unknownsChanged = false
	if all {
		s.fullIdleChecks++
		return nil, true
	}
	return dirty, false
}

// expireContextsLocked drops the kept contexts idle for at least retention, oldest first, with the retention setting of
// now; with no retention, every idle one, also those whose idle period a clock that went back makes start after now.
func (s *TaskScheduler) expireContextsLocked(now, retention float64) {
	for {
		conversationId, ok := s.idleOrder.First()
		if !ok {
			return
		}
		kept := s.contexts[conversationId]
		if retention > 0 && kept != nil && kept.idleSince != nil && now-*kept.idleSince < retention {
			return
		}
		s.dropContextLocked(conversationId)
	}
}

// markIdleLocked starts the idle period of a kept context in the expiry order. An idle period older than the newest one
// kept, which a clock that went back starts, sorts the order again, and the newest is taken from what is kept then.
func (s *TaskScheduler) markIdleLocked(conversationId durable.ConversationId, idleSince float64) {
	s.idleOrder.Delete(conversationId)
	s.idleOrder.Add(conversationId)
	if s.idleOrder.Len() == 1 || idleSince >= s.idleNewest {
		s.idleNewest = idleSince
		return
	}
	sorted := s.idleOrder.Values()
	slices.SortStableFunc(sorted, func(a, b durable.ConversationId) int {
		return compareFloat(*s.contexts[a].idleSince, *s.contexts[b].idleSince)
	})
	s.idleOrder.Clear()
	for _, id := range sorted {
		s.idleOrder.Add(id)
	}
	s.idleNewest = *s.contexts[sorted[len(sorted)-1]].idleSince
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// dropContextLocked forgets a kept context, idle or not, and its place in the expiry order.
func (s *TaskScheduler) dropContextLocked(conversationId durable.ConversationId) {
	delete(s.contexts, conversationId)
	s.idleOrder.Delete(conversationId)
}

// SchedulerIdleState is the kept contexts' idle state, for tests: how many are kept and how many are idle, how many
// disagree with a full recompute, and how many idle checks covered every scope.
type SchedulerIdleState struct {
	Contexts       int
	IdleContexts   int
	IdleMismatches int
	FullIdleChecks int
}

// idleState computes the kept contexts' idle state on demand (scheduler.ts idleState): an idle period without inactiveLocked,
// or inactiveLocked without one, and an expiry order that differs from the idle periods or is not sorted, are mismatches.
func (s *TaskScheduler) idleState() SchedulerIdleState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := SchedulerIdleState{Contexts: len(s.contexts), FullIdleChecks: s.fullIdleChecks}
	for conversationId, kept := range s.contexts {
		if kept.idleSince != nil {
			state.IdleContexts++
		}
		if (kept.idleSince != nil) != s.inactiveLocked(conversationId) {
			state.IdleMismatches++
		}
		if s.idleOrder.Has(conversationId) != (kept.idleSince != nil) {
			state.IdleMismatches++
		}
	}
	previous := math.Inf(-1)
	for _, conversationId := range s.idleOrder.Values() {
		kept := s.contexts[conversationId]
		if kept == nil || kept.idleSince == nil || *kept.idleSince < previous {
			state.IdleMismatches++
			continue
		}
		previous = *kept.idleSince
	}
	return state
}
