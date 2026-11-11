// Ports packages/durable/src/harness/scheduler.ts (decision regions and the ownership memberships of group commit,
// docs/group-commit.md §4.2 and §6).
//
// Go mapping: a decision region is a blocking call on the Session line. It runs decide synchronously under the
// scheduler's mutex with s.reading set, so the helpers record the keys they read (recordLocked); decide and the touches of
// the recorded keys happen under one hold of the mutex, so no publication's observe runs between them (the Session removes a
// part's keys from its unpublished set only after its internal listeners ran). When a key has an unpublished writer the
// plan, or the error, is dropped, the mutex is released while the region waits for every gate, and refresh then reloads
// what the region needs before decide runs again. A waiting job holds the line, so unpublished writes only shrink and the
// runs converge. No goroutine is started: the region runs on the job's own. Every "Locked" function in this package
// is called with the mutex held and may release it only while it waits for the Session (a gate, a gated read), as upstream's
// awaits let other tasks of the event loop run.

package harness

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// regionLocked runs decide as a decision region (group commit §6): synchronous, reading scheduler memory and recording the
// keys it reads, staging nothing and changing no scheduler state. When a key it read has an unpublished writer, its
// result, or its error, is dropped: once that writer is published, refresh reloads what the region needs and it runs
// again. What a region read that had no unpublished writer gets none for the rest of the job, so its result stands while
// the job applies it. s.mu is held on entry and on return.
//
// A gate it waits for is a wait of the Session (session.Wait): the delivery that runs it has entered; ctx does not end the wait, as Pi's does not.
func regionLocked[T any](ctx context.Context, s *TaskScheduler, decide func() (T, error), refresh func() error) (T, error) {
	for {
		keys := map[string]struct{}{}
		s.reading = keys
		value, err := func() (T, error) {
			defer func() { s.reading = nil }()
			return decide()
		}()
		var gates []<-chan struct{}
		for key := range keys {
			if gate := s.options.Session.Touch(key); gate != nil {
				gates = append(gates, gate)
			}
		}
		if len(gates) == 0 {
			return value, err
		}
		failure := s.waitGatesLocked(ctx, gates)
		if failure != nil {
			var zero T
			return zero, failure
		}
		if refresh != nil {
			if err := refresh(); err != nil {
				var zero T
				return zero, err
			}
		}
	}
}

// waitGatesLocked releases s.mu while it waits for every gate to open (a wait of the Session: the delivery that calls it has
// entered), then checks the Session is still healthy. Pi's region awaits the gate without the caller's signal: a gate always opens,
// by a publication or by the Session's failure, so the wait does not end with ctx (context.WithoutCancel keeps the Continuation ctx carries).
func (s *TaskScheduler) waitGatesLocked(ctx context.Context, gates []<-chan struct{}) error {
	s.mu.Unlock()
	defer s.mu.Lock()
	ctx = context.WithoutCancel(ctx)
	for _, gate := range gates {
		if err := session.Wait(ctx, gate); err != nil {
			return err
		}
	}
	return s.options.Session.AssertHealthy()
}

// withoutLock runs f with s.mu released, for work that may wait for the Session: a transaction's reads and the Harness
// cleanup a task's end stages wait for the commits they read behind, which publish through this scheduler.
func (s *TaskScheduler) withoutLock(f func() error) error {
	s.mu.Unlock()
	defer s.mu.Lock()
	return f()
}

// regionPlainLocked is a region whose decision cannot fail.
func regionPlainLocked[T any](s *TaskScheduler, decide func() T, refresh func() error) (T, error) {
	return regionPlainCtxLocked(context.Background(), s, decide, refresh)
}

// regionPlainCtxLocked is regionPlainLocked for a region whose caller's ctx carries the Continuation its wait enters.
func regionPlainCtxLocked[T any](ctx context.Context, s *TaskScheduler, decide func() T, refresh func() error) (T, error) {
	return regionLocked(ctx, s, func() (T, error) { return decide(), nil }, refresh)
}

// publishedLocked waits, on the line, until every commit admitted before is published: for a decision that reads what
// cannot be keyed, every live task (open, inspection) or every task a conversation abort reaches. s.mu is held on entry
// and on return.
func (s *TaskScheduler) publishedLocked() error {
	gate := s.options.Session.Touch(session.AllKeys)
	if gate == nil {
		return nil
	}
	return s.waitGatesLocked(context.Background(), []<-chan struct{}{gate})
}

// published is publishedLocked for a caller that does not hold s.mu.
func (s *TaskScheduler) published() error {
	gate := s.options.Session.Touch(session.AllKeys)
	if gate == nil {
		return nil
	}
	if err := session.Wait(context.Background(), gate); err != nil {
		return err
	}
	return s.options.Session.AssertHealthy()
}

// reloadScopesLocked loads the chains a region walks, again after it waited: what was published meanwhile may list new work.
func (s *TaskScheduler) reloadScopesLocked() error {
	_, err := s.loadScopesLocked(false)
	return err
}

// recordLocked notes, in a decision region, that it reads key; anywhere else, nothing.
func (s *TaskScheduler) recordLocked(key string) {
	if s.reading != nil {
		s.reading[key] = struct{}{}
	}
}

// liveRecordLocked is a live task's committed record, read by a decision region.
func (s *TaskScheduler) liveRecordLocked(id durable.TaskId) (anyTaskRecord, bool) {
	s.recordLocked(session.TaskKey(id))
	record, ok := s.live[id]
	return record, ok
}

// pinLocked keeps node, which a chain load of the job on the line installed, until that job's commit is published or
// fails: work it creates below the node needs it, and the sweep would drop it as unneeded before then.
func (s *TaskScheduler) pinLocked(node treeNode) {
	s.pins[node]++
	s.options.Session.OnJobEnd(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if count := s.pins[node] - 1; count > 0 {
			s.pins[node] = count
			return
		}
		delete(s.pins, node)
		s.dropIfUnneededLocked(node)
	})
}

// intentChanges reports whether replacing previous (absent: a new task) with next changes the cancellation intent set or
// what it means: a mark set, upgraded, or cleared, or a held or ended outcome other than completed.
func intentChanges(previous *anyTaskRecord, next anyTaskRecord) bool {
	if (previous != nil && previous.AbortRequested) != next.AbortRequested {
		return true
	}
	if previous != nil && previous.AbortReason != next.AbortReason || previous == nil && next.AbortReason != "" {
		return true
	}
	if (previous != nil && failedOutcome(*previous)) != failedOutcome(next) {
		return true
	}
	return (previous != nil && cancellationIntent(*previous)) != cancellationIntent(next)
}

// MembershipKeys is the ownership memberships a commit's task and conversation changes write (group commit §4.2), at its
// admission on the line: for each task created or made terminal, every node from its parent up to and including the nearest
// live task, or the root conversation, and then the roots too, conservatively, as a pending end may unlist the root a
// creation finds listed; for each conversation created under a task, that task; and the intent set when a change sets,
// upgrades, or clears a mark or a held or ended outcome other than completed. It loads the chains it walks; the nodes it
// installs stay until the commit is published.
func (s *TaskScheduler) MembershipKeys(tx *session.Transaction) ([]string, error) {
	tasks := tx.StagedTasks()
	conversations := tx.StagedConversations()
	if len(tasks) == 0 && !slices.ContainsFunc(conversations, func(record durable.ConversationRecord) bool { return record.Owner != nil }) {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := map[string]struct{}{}
	var order []string
	add := func(key string) {
		if _, seen := keys[key]; !seen {
			keys[key] = struct{}{}
			order = append(order, key)
		}
	}
	staged := overlayOf(tx)
	created := map[durable.TaskId]struct{}{}
	for _, id := range tx.CreatedTaskIds() {
		created[id] = struct{}{}
	}
	reached := map[string]*durable.ConversationId{}
	for _, record := range tasks {
		var previous *anyTaskRecord
		if live, ok := s.live[record.Id]; ok {
			previous = &live
		}
		if intentChanges(previous, record) {
			add(session.IntentKey)
		}
		if _, isNew := created[record.Id]; !isNew && record.State.Status != durable.TaskTerminal {
			continue
		}
		if err := s.loadChainLocked(parentOf(nodeOf(record)), staged); err != nil {
			return nil, err
		}
		root, err := s.chainKeysLocked(parentTreeNode(nodeOf(record)), staged, add, reached)
		if err != nil {
			return nil, err
		}
		if root != nil {
			add(session.RootsKey)
		}
	}
	for _, conversation := range conversations {
		if conversation.Owner == nil {
			continue
		}
		owner := conversation.Owner.TaskId
		if err := s.loadChainLocked(up{isTask: true, task: owner}, staged); err != nil {
			return nil, err
		}
		if _, err := s.chainKeysLocked(taskTreeNode(owner), staged, add, reached); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// chainKeysLocked adds the memberships from start up to and including the nearest live task, or the root conversation,
// which it returns. reached keeps, per node walked, where its walk ended, so a chain several tasks of a commit share is
// walked once. Whether an ancestor is live can change under a pending commit, so it is read after a touch of the
// ancestor's record: a pending end must not stop the chain early. Ownership fields are immutable, so not keyed.
func (s *TaskScheduler) chainKeysLocked(start treeNode, staged *overlay, add func(string), reached map[string]*durable.ConversationId) (*durable.ConversationId, error) {
	var walked []string
	var root *durable.ConversationId
	node, ok := start, true
	for ok {
		key := ownedKey(node)
		// Walked up from here already, for another task of the commit.
		if found, seen := reached[key]; seen {
			root = found
			break
		}
		add(key)
		walked = append(walked, key)
		if node.conversation {
			conversationId := durable.ConversationId(node.id)
			edge, known := s.edgeLocked(conversationId, staged)
			switch {
			case !known:
				ok = false
			case edge == nil:
				root = &conversationId
				ok = false
			default:
				node = taskTreeNode(*edge)
			}
			continue
		}
		id := durable.TaskId(node.id)
		record, found := staged.tasks[id]
		if !found {
			if gate := s.options.Session.Touch(session.TaskKey(id)); gate != nil {
				if err := s.waitGatesLocked(context.Background(), []<-chan struct{}{gate}); err != nil {
					return nil, err
				}
				// It may have ended meanwhile, and its node been swept: load the chain above it again.
				if err := s.loadChainLocked(up{isTask: true, task: id}, staged); err != nil {
					return nil, err
				}
			}
			record, found = s.live[id]
		}
		if found && record.State.Status != durable.TaskTerminal {
			break
		}
		var above taskNode
		if found {
			above = nodeOf(record)
		} else if settled, isSettled := s.settled[id]; isSettled {
			above = settled
		} else {
			ok = false
			continue
		}
		node = parentTreeNode(above)
	}
	for _, key := range walked {
		reached[key] = root
	}
	return root, nil
}
