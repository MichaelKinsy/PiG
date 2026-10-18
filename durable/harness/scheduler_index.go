package harness

// Ports packages/durable/src/harness/scheduler.ts (the in-memory indexes of upstream cb437549): the ownership tree
// downward around live work, and the sets that hold each reconcile pass's candidates. No pass visits every live task.

import (
	"github.com/MichaelKinsy/PiG/durable"
)

// orderedSet is a JavaScript Set: insertion order, with an O(1) add, delete, and membership test. Deleting and adding a
// key again moves it to the end, as Set does.
type orderedSet[K comparable] struct {
	index map[K]int
	items []setItem[K]
	dead  int
	// ranging counts the iterations in progress; a slot is reclaimed only while none is.
	ranging int
}

type setItem[K comparable] struct {
	key  K
	live bool
}

func newOrderedSet[K comparable]() *orderedSet[K] { return &orderedSet[K]{index: map[K]int{}} }

func (set *orderedSet[K]) Has(key K) bool {
	_, ok := set.index[key]
	return ok
}

func (set *orderedSet[K]) Len() int { return len(set.index) }

// Add adds key unless present.
func (set *orderedSet[K]) Add(key K) {
	if _, ok := set.index[key]; ok {
		return
	}
	set.index[key] = len(set.items)
	set.items = append(set.items, setItem[K]{key: key, live: true})
}

// Delete removes key.
func (set *orderedSet[K]) Delete(key K) {
	at, ok := set.index[key]
	if !ok {
		return
	}
	delete(set.index, key)
	set.items[at].live = false
	set.dead++
	if set.ranging == 0 && set.dead > 16 && set.dead > len(set.items)/2 {
		set.compact()
	}
}

func (set *orderedSet[K]) compact() {
	kept := set.items[:0]
	for _, item := range set.items {
		if item.live {
			set.index[item.key] = len(kept)
			kept = append(kept, item)
		}
	}
	clear(set.items[len(kept):])
	set.items = kept
	set.dead = 0
}

// Clear removes every key.
func (set *orderedSet[K]) Clear() {
	clear(set.index)
	clear(set.items)
	set.items = set.items[:0]
	set.dead = 0
}

// Values returns the keys in insertion order.
func (set *orderedSet[K]) Values() []K {
	keys := make([]K, 0, len(set.index))
	for _, item := range set.items {
		if item.live {
			keys = append(keys, item.key)
		}
	}
	return keys
}

// Range calls visit with each key in insertion order until it returns false. Keys added during the iteration are
// visited; the set must not be compacted underneath it, so deletions leave their slots until it ends.
func (set *orderedSet[K]) Range(visit func(K) bool) {
	set.ranging++
	defer func() { set.ranging-- }()
	for at := 0; at < len(set.items); at++ {
		if item := set.items[at]; item.live && !visit(item.key) {
			return
		}
	}
}

// treeNode is a node of the downward ownership index: a task, or a conversation.
type treeNode struct {
	conversation bool
	id           int64
}

func taskTreeNode(id durable.TaskId) treeNode { return treeNode{id: int64(id)} }

func conversationTreeNode(id durable.ConversationId) treeNode {
	return treeNode{conversation: true, id: int64(id)}
}

// parentTreeNode is the node a task hangs from: its owner task, or its conversation.
func parentTreeNode(node taskNode) treeNode {
	if node.owner != nil {
		return taskTreeNode(*node.owner)
	}
	return conversationTreeNode(node.conversationId)
}

func addToSet[K, V comparable](sets map[K]*orderedSet[V], key K, value V) {
	set := sets[key]
	if set == nil {
		set = newOrderedSet[V]()
		sets[key] = set
	}
	set.Add(value)
}

func deleteFromSet[K, V comparable](sets map[K]*orderedSet[V], key K, value V) {
	set := sets[key]
	if set == nil {
		return
	}
	set.Delete(value)
	if set.Len() == 0 {
		delete(sets, key)
	}
}

// walkOptions selects how walkDownLocked proceeds.
type walkOptions struct {
	staged          *overlay
	crossBackground bool
	// enter, when set, rejects the tasks a walk does not go below.
	enter func(durable.TaskId) bool
}

// walkDownLocked visits the tasks below start that a walk down reaches: those below lists and, under an overlay, the
// commit's new tasks, with its candidates in place of committed records. Background tasks are visited but not entered,
// unless crossBackground, and neither are the tasks enter rejects. live is the record when the task is live. visit
// returns false to stop. A loop, as chains can be deeper than the call stack.
func (s *TaskScheduler) walkDownLocked(start treeNode, options walkOptions, visit func(id durable.TaskId, node taskNode, live bool, record anyTaskRecord) bool) {
	var links map[treeNode]*orderedSet[treeNode]
	if options.staged != nil {
		links = s.linksLocked(options.staged)
	}
	pending := []treeNode{start}
	for len(pending) > 0 {
		at := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		stopped := false
		each := func(children *orderedSet[treeNode]) {
			if children == nil || stopped {
				return
			}
			children.Range(func(child treeNode) bool {
				if child.conversation {
					pending = append(pending, child)
					return true
				}
				id := durable.TaskId(child.id)
				var node taskNode
				var record anyTaskRecord
				found := false
				if options.staged != nil {
					record, found = options.staged.tasks[id]
				}
				if !found {
					record, found = s.live[id]
				}
				isLive := found && record.State.Status != durable.TaskTerminal
				if found {
					node = nodeOf(record)
				} else if settled, ok := s.settled[id]; ok {
					node = settled
				} else {
					return true
				}
				if !visit(id, node, isLive, record) {
					stopped = true
					return false
				}
				if (options.crossBackground || !node.background) && (options.enter == nil || options.enter(id)) {
					pending = append(pending, child)
				}
				return true
			})
		}
		each(s.below[at])
		each(links[at])
		if stopped {
			return
		}
	}
}

// linksLocked returns the links walks down need for the commit's new tasks: each one under its parent node, then each
// node above it that below does not list yet, until one it lists, a live task (always listed), or the chain's end.
func (s *TaskScheduler) linksLocked(staged *overlay) map[treeNode]*orderedSet[treeNode] {
	if staged.links != nil {
		return staged.links
	}
	links := map[treeNode]*orderedSet[treeNode]{}
	for _, id := range staged.order {
		record := staged.tasks[id]
		if _, live := s.live[id]; live {
			continue
		}
		if _, settled := s.settled[id]; settled {
			continue
		}
		child := taskTreeNode(id)
		parent, hasParent := parentTreeNode(nodeOf(record)), true
		for hasParent && (links[parent] == nil || !links[parent].Has(child)) {
			addToSet(links, parent, child)
			if !parent.conversation {
				parentId := durable.TaskId(parent.id)
				if _, live := s.live[parentId]; live {
					break
				}
				if _, staged := staged.tasks[parentId]; staged {
					break
				}
			}
			var above treeNode
			hasAbove := false
			if parent.conversation {
				if edge, known := s.edgeLocked(durable.ConversationId(parent.id), staged); known && edge != nil {
					above, hasAbove = taskTreeNode(*edge), true
				}
			} else if fields, ok := s.settled[durable.TaskId(parent.id)]; ok {
				above, hasAbove = parentTreeNode(fields), true
			}
			if hasAbove && s.below[above] != nil && s.below[above].Has(parent) {
				break
			}
			child, parent, hasParent = parent, above, hasAbove
		}
	}
	staged.links = links
	return links
}

// ownedLiveLocked returns the live ordinary owned work of owner (spec §5.5): the live non-background tasks below it that
// a walk down reaches without entering a background task; with first, only the first. Candidates in staged replace
// committed records; the walk does not enter tasks in done, which have none. Owner chains must be loaded.
func (s *TaskScheduler) ownedLiveLocked(owner durable.TaskId, staged *overlay, first bool, done map[durable.TaskId]bool) []durable.TaskId {
	options := walkOptions{staged: staged}
	if done != nil {
		options.enter = func(id durable.TaskId) bool { return !done[id] }
	}
	var found []durable.TaskId
	s.walkDownLocked(taskTreeNode(owner), options, func(id durable.TaskId, node taskNode, live bool, _ anyTaskRecord) bool {
		if !live || node.background {
			return true
		}
		found = append(found, id)
		return !first
	})
	return found
}

func (s *TaskScheduler) hasOwnedLiveLocked(owner durable.TaskId, staged *overlay, done map[durable.TaskId]bool) bool {
	return len(s.ownedLiveLocked(owner, staged, true, done)) > 0
}

// knownLocked reports whether the chain above node is loaded, without walking: known tasks outside unloaded have a
// loaded chain.
func (s *TaskScheduler) knownLocked(node treeNode) bool {
	for {
		if !node.conversation {
			id := durable.TaskId(node.id)
			_, live := s.live[id]
			_, settled := s.settled[id]
			return (live || settled) && !s.unloaded.Has(id)
		}
		owner, ok := s.edges[durable.ConversationId(node.id)]
		if !ok {
			return false
		}
		if owner == nil {
			return true
		}
		node = taskTreeNode(*owner)
	}
}

// nearestLiveAboveLocked returns the nearest live task above ended ordinary work node, in the overlay's view: the one
// that work held, if any. Work at or below a background task holds nothing above it.
func (s *TaskScheduler) nearestLiveAboveLocked(node taskNode, staged *overlay) (anyTaskRecord, bool) {
	for current := node; !current.background; {
		var owner *durable.TaskId
		if current.owner != nil {
			owner = current.owner
		} else if edge, known := s.edgeLocked(current.conversationId, staged); known {
			owner = edge
		}
		if owner == nil {
			return anyTaskRecord{}, false
		}
		record, found := anyTaskRecord{}, false
		if staged != nil {
			record, found = staged.tasks[*owner]
		}
		if !found {
			record, found = s.live[*owner]
		}
		if found && record.State.Status != durable.TaskTerminal {
			return record, true
		}
		if found {
			current = nodeOf(record)
			continue
		}
		above, ok := s.settled[*owner]
		if !ok {
			return anyTaskRecord{}, false
		}
		current = above
	}
	return anyTaskRecord{}, false
}

// releaseLocked notes that the nearest live task above ended ordinary work may be free now: one holding its outcome,
// or an aborting one.
func (s *TaskScheduler) releaseLocked(node taskNode, staged *overlay) (anyTaskRecord, bool) {
	above, ok := s.nearestLiveAboveLocked(node, staged)
	if !ok {
		return above, false
	}
	if above.State.Status == durable.TaskCompleting {
		s.finalizeChecks.Add(above.Id)
	} else if above.AbortRequested {
		s.runnable.Add(above.Id)
	}
	return above, true
}

// settleLocked keeps a terminal task a chain load read, so walks pass through it; its own chain is not loaded yet.
func (s *TaskScheduler) settleLocked(id durable.TaskId, node taskNode) {
	s.settled[id] = node
	s.unloaded.Add(id)
	s.relistLocked(taskTreeNode(id))
	s.dropIfUnneededLocked(taskTreeNode(id))
}

// dropIfUnneededLocked queues an ended task or a conversation for the sweep when nothing is listed below it.
func (s *TaskScheduler) dropIfUnneededLocked(node treeNode) {
	if !node.conversation {
		if _, live := s.live[durable.TaskId(node.id)]; live {
			return
		}
	}
	if _, listed := s.below[node]; listed {
		return
	}
	s.dropQueue.Add(node)
	s.scheduleSweepLocked()
}

// scheduleSweepLocked sweeps in a job of its own on the Session line, after the operation underway: a rejected commit,
// one with nothing to write, and a read publish nothing, so the sweep at the end of observe would not run for what they
// loaded.
func (s *TaskScheduler) scheduleSweepLocked() {
	if s.sweepScheduled || s.sealing.Load() {
		return
	}
	// Taken now, in the turn that scheduled it, as upstream's queued job is.
	ticket := s.options.Session.TakeLineTicket()
	if ticket == nil {
		return
	}
	s.sweepScheduled = true
	s.background.Go(func() {
		// Fails only once the Session is closing or failed, when nothing is kept anyway.
		_, _ = s.options.Session.ReadWithTicket(ticket, func() (any, error) {
			s.mu.Lock()
			s.sweepScheduled = false
			s.sweepLocked()
			s.mu.Unlock()
			return nil, nil
		})
	})
}

// sweepLocked drops the queued nodes that are still unneeded: not live, nothing listed below. It runs once a publication
// is fully tracked, and as a job of its own on the line, never inside an operation: work a commit creates below a chain
// its own callback loaded keeps that chain. A flat loop: unlisting already queued every node on the way up that lost its
// last listed node.
func (s *TaskScheduler) sweepLocked() {
	s.dropQueue.Range(func(node treeNode) bool {
		if _, listed := s.below[node]; listed {
			return true
		}
		if node.conversation {
			delete(s.edges, durable.ConversationId(node.id))
			s.roots.Delete(durable.ConversationId(node.id))
		} else if _, live := s.live[durable.TaskId(node.id)]; !live {
			delete(s.settled, durable.TaskId(node.id))
			s.unloaded.Delete(durable.TaskId(node.id))
		}
		return true
	})
	s.dropQueue.Clear()
}

// trackLocked brings the scheduler's sets in line with a task's committed record. A task that ended may free the
// nearest live task above it: one holding its outcome finalizes, an abort-marked one runs its abort handler; its waiters
// may run.
func (s *TaskScheduler) trackLocked(previous *anyTaskRecord, record anyTaskRecord) {
	id := record.Id
	if previous != nil && previous.State.Status == durable.TaskWaiting {
		for _, on := range previous.State.On {
			deleteFromSet(s.waiters, on, id)
		}
	}
	if record.State.Status == durable.TaskTerminal {
		node := nodeOf(record)
		s.deleteLiveLocked(id)
		s.sleeping.Delete(id)
		s.intent.Delete(id)
		s.runnable.Delete(id)
		s.finalizeChecks.Delete(id)
		s.settled[id] = node
		// Before anything is forgotten: the walk up to the task it held passes the ended tasks between them. Its chain is
		// loaded, as every commit that ends a task loads the chains of unloaded first.
		s.releaseLocked(node, nil)
		if waiting := s.waiters[id]; waiting != nil {
			waiting.Range(func(waiter durable.TaskId) bool {
				s.runnable.Add(waiter)
				return true
			})
		}
		s.relistLocked(taskTreeNode(id))
		s.dropIfUnneededLocked(taskTreeNode(id))
		return
	}
	s.setLiveLocked(record)
	if previous == nil {
		if !s.knownLocked(parentTreeNode(nodeOf(record))) {
			s.unloaded.Add(id)
		}
		s.relistLocked(taskTreeNode(id))
	}
	if cancellationIntent(record) {
		s.intent.Add(id)
	} else {
		s.intent.Delete(id)
	}
	if record.State.Status == durable.TaskCompleting {
		s.runnable.Delete(id)
		s.finalizeChecks.Add(id)
	} else {
		s.runnable.Add(id)
	}
	if record.State.Status == durable.TaskWaiting {
		for _, on := range record.State.On {
			addToSet(s.waiters, on, id)
		}
	}
	if record.State.Status == durable.TaskSleeping {
		s.sleeping.Add(id)
	} else {
		s.sleeping.Delete(id)
	}
}

// relistLocked lists start under its parent node in below while it is a live task or has nodes listed below it, and
// unlists it otherwise; then its parent, as far up as that changes. So below holds exactly the tree around live work; an
// parent that loses its last listed node is queued for the sweep. A loop, as ownership chains can be deeper than the call
// stack.
func (s *TaskScheduler) relistLocked(start treeNode) {
	node := start
	for {
		_, live := s.live[durable.TaskId(node.id)]
		needed := (!node.conversation && live) || s.below[node] != nil
		var parent treeNode
		rootless := false
		if node.conversation {
			edge, known := s.edges[durable.ConversationId(node.id)]
			if !known {
				return
			}
			if edge == nil {
				rootless = true
			} else {
				parent = taskTreeNode(*edge)
			}
		} else {
			id := durable.TaskId(node.id)
			var fields taskNode
			if record, ok := s.live[id]; ok {
				fields = nodeOf(record)
			} else if settled, ok := s.settled[id]; ok {
				fields = settled
			} else {
				return
			}
			parent = parentTreeNode(fields)
		}
		if rootless {
			if needed {
				s.roots.Add(durable.ConversationId(node.id))
			} else {
				s.roots.Delete(durable.ConversationId(node.id))
			}
			return
		}
		listed := s.below[parent] != nil && s.below[parent].Has(node)
		if listed == needed {
			return
		}
		if needed {
			first := s.below[parent] == nil
			addToSet(s.below, parent, node)
			if !first {
				return
			}
		} else {
			deleteFromSet(s.below, parent, node)
			if s.below[parent] != nil {
				return
			}
			s.dropIfUnneededLocked(parent)
		}
		node = parent
	}
}

// schedulerIndexSizes are the sizes of the scheduler's ownership indexes, for tests that bound its memory.
type schedulerIndexSizes struct{ live, settled, edges, below, roots, unloaded, dropQueue int }

func (s *TaskScheduler) indexSizes() schedulerIndexSizes {
	s.mu.Lock()
	defer s.mu.Unlock()
	below := 0
	for _, children := range s.below {
		below += children.Len()
	}
	return schedulerIndexSizes{live: len(s.live), settled: len(s.settled), edges: len(s.edges), below: below, roots: s.roots.Len(), unloaded: s.unloaded.Len(), dropQueue: s.dropQueue.Len()}
}
