package session

// Ports packages/durable/src/harness/scheduler.ts (the in-memory indexes of upstream cb437549): the ownership tree
// downward around live work, and the sets that hold each reconcile pass's candidates. No pass visits every live task.

import "github.com/MichaelKinsy/PiG/durable/core/jv"

// orderedSet is a JavaScript Set: insertion order, with an O(1) add, delete, and membership test. Deleting and adding a
// key again moves it to the end, as Set does.
//
// Its slots live in chunks of setChunk, each with its own index map once the set has more than smallSet slots, so no
// set allocates one block larger than a chunk: the TinyGo core's non-moving allocator fails a large slice or map growth
// in a fragmented heap that has plenty of memory free in smaller ranges (U-harness-scheduler-retention's 20,000-deep
// chain trapped "out of memory" at 96 MiB growing one set's slots to 512 KiB with 33 MB free).
type orderedSet[K comparable] struct {
	// chunks holds the slots in order; every chunk but the last is full. The first grows by append, so a small set stays small.
	chunks [][]setItem[K]
	// index maps each key to its slot, one map per chunk, once the set has more than smallSet slots; a smaller set scans.
	index []map[K]int
	slots int // the slots in use, live or dead
	n     int // the keys in the set
	dead  int
	// ranging counts the iterations in progress; a slot is reclaimed only while none is.
	ranging int
}

// smallSet is the number of slots up to which a set finds a key by scanning them. Most sets of the ownership index hold one
// child, and an index map for each cost about 250 bytes of a node's memory on the TinyGo core (U-harness-scheduler-retention's
// 20,000-deep chain lists 20,000 of them).
const smallSet = 8

// setChunk is the number of slots in a chunk: a chunk of tree nodes is 24 KiB and its index map about the same.
const setChunk = 1024

type setItem[K comparable] struct {
	key  K
	live bool
}

func newOrderedSet[K comparable]() *orderedSet[K] { return &orderedSet[K]{} }

// item returns the slot at.
//
//portlint:allow numbers slot-to-chunk arithmetic on a non-negative Go int slot position of the Go-only chunked storage; a JS Set has no chunks and no JS number is involved
func (set *orderedSet[K]) item(at int) *setItem[K] { return &set.chunks[at/setChunk][at%setChunk] }

// find returns the slot of key.
func (set *orderedSet[K]) find(key K) (int, bool) {
	if set.index != nil {
		for _, index := range set.index {
			if at, ok := index[key]; ok {
				return at, true
			}
		}
		return 0, false
	}
	for at := range set.slots {
		if item := set.item(at); item.live && item.key == key {
			return at, true
		}
	}
	return 0, false
}

func (set *orderedSet[K]) Has(key K) bool {
	_, ok := set.find(key)
	return ok
}

func (set *orderedSet[K]) Len() int { return set.n }

// Add adds key unless present.
func (set *orderedSet[K]) Add(key K) {
	if _, ok := set.find(key); ok {
		return
	}
	set.push(key)
	set.n++
}

// push appends a live slot for key and indexes it.
func (set *orderedSet[K]) push(key K) {
	at := set.slots
	//portlint:allow numbers slot-to-chunk arithmetic on a non-negative Go int slot position of the Go-only chunked storage; a JS Set has no chunks and no JS number is involved
	c := at / setChunk
	if c == len(set.chunks) {
		set.chunks = append(set.chunks, nil)
	}
	chunk := set.chunks[c]
	if len(chunk) == cap(chunk) {
		// A later chunk is made at its full size when it starts; the first doubles up to a chunk, so a one-child set
		// stays small. One make serves both, one allocation site per instantiation (budget/allocs.json).
		size := setChunk
		if c == 0 {
			size = min(max(2*cap(chunk), 1), setChunk)
		}
		grown := make([]setItem[K], len(chunk), size)
		copy(grown, chunk)
		chunk = grown
	}
	set.chunks[c] = append(chunk, setItem[K]{key: key, live: true})
	set.slots++
	switch {
	case set.index != nil:
		if c == len(set.index) {
			set.index = append(set.index, map[K]int{})
		}
		set.index[c][key] = at
	case set.slots > smallSet:
		set.buildIndex()
	}
}

// Delete removes key.
func (set *orderedSet[K]) Delete(key K) {
	at, ok := set.find(key)
	if !ok {
		return
	}
	if set.index != nil {
		//portlint:allow numbers slot-to-chunk arithmetic on a non-negative Go int slot position of the Go-only chunked storage; a JS Set has no chunks and no JS number is involved
		delete(set.index[at/setChunk], key)
	}
	set.item(at).live = false
	set.n--
	set.dead++
	//portlint:allow numbers a Go-side compaction threshold over slot counts, not a JS number: nothing Pi observes depends on when the set compacts
	if set.ranging == 0 && set.dead > 16 && set.dead > set.slots/2 {
		set.compact()
	}
}

// compact drops the dead slots, keeping the live keys in order. It moves them into new chunks one at a time, so it never
// allocates a block larger than a chunk either.
func (set *orderedSet[K]) compact() {
	old := set.chunks
	indexed := set.index != nil
	set.chunks, set.index, set.slots, set.dead = nil, nil, 0, 0
	for _, chunk := range old {
		for _, item := range chunk {
			if item.live {
				set.push(item.key)
			}
		}
	}
	if indexed && set.index == nil {
		set.buildIndex()
	}
}

// buildIndex indexes every live slot, one map per chunk.
func (set *orderedSet[K]) buildIndex() {
	set.index = set.index[:0]
	for c, chunk := range set.chunks {
		set.index = append(set.index, make(map[K]int, len(chunk)))
		for i, item := range chunk {
			if item.live {
				set.index[c][item.key] = c*setChunk + i
			}
		}
	}
}

// reset empties the slots and the index; n is the caller's.
func (set *orderedSet[K]) reset() {
	clear(set.chunks)
	set.chunks = set.chunks[:0]
	set.index = nil
	set.slots = 0
	set.dead = 0
}

// Clear removes every key.
func (set *orderedSet[K]) Clear() {
	set.reset()
	set.n = 0
}

// Values returns the keys in insertion order.
func (set *orderedSet[K]) Values() []K {
	keys := make([]K, 0, set.n)
	for at := range set.slots {
		if item := set.item(at); item.live {
			keys = append(keys, item.key)
		}
	}
	return keys
}

// Range calls visit with each key in insertion order until it returns false. Keys added during the iteration are
// visited; deletions leave their slots until it ends.
func (set *orderedSet[K]) Range(visit func(K) bool) {
	set.ranging++
	defer set.rangeDone()
	for at := 0; at < set.slots; at++ {
		if item := set.item(at); item.live && !visit(item.key) {
			return
		}
	}
}

// rangeDone ends a Range. Deferred as a method call, it needs no closure on the heap.
func (set *orderedSet[K]) rangeDone() { set.ranging-- }

// treeNode is a node of the downward ownership index: a task, or a conversation.
type treeNode struct {
	conversation bool
	id           ID
}

func taskTreeNode(id ID) treeNode         { return treeNode{id: id} }
func conversationTreeNode(id ID) treeNode { return treeNode{conversation: true, id: id} }

// parentTreeNode is the node a task hangs from: its owner task, or its conversation.
func parentTreeNode(n taskNode) treeNode {
	if n.owner != 0 {
		return taskTreeNode(n.owner)
	}
	return conversationTreeNode(n.conversationID)
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

// walkOptions selects how walkDown proceeds.
type walkOptions struct {
	o               *overlay
	crossBackground bool
	// enter, when set, rejects the tasks a walk does not go below.
	enter func(ID) bool
}

// walkDown visits the tasks below start that a walk down reaches: those below lists and, under an overlay, the commit's
// new tasks, with its candidates in place of committed records. Background tasks are visited but not entered, unless
// crossBackground, and neither are the tasks enter rejects. rec is the record when the task is live, nil otherwise.
// visit returns false to stop. A loop, as chains can be deeper than the call stack.
func (s *Scheduler) walkDown(start treeNode, opts walkOptions, visit func(id ID, n taskNode, rec *jv.Object) bool) {
	var links map[treeNode]*orderedSet[treeNode]
	if opts.o != nil {
		links = s.links(opts.o)
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
				var rec *jv.Object
				found := false
				if opts.o != nil {
					rec, found = opts.o.tasks[child.id]
				}
				if !found {
					rec, found = s.live.get(child.id)
				}
				var n taskNode
				var live *jv.Object
				if found {
					n = nodeOf(rec)
					if taskStatus(rec) != StatusTerminal {
						live = rec
					}
				} else if settled, ok := s.settled[child.id]; ok {
					n = settled
				} else {
					return true
				}
				if !visit(child.id, n, live) {
					stopped = true
					return false
				}
				if (opts.crossBackground || !n.background) && (opts.enter == nil || opts.enter(child.id)) {
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

// links returns the links walks down need for the commit's new tasks: each one under its parent node, then each node
// above it that below does not list yet, until one it lists, a live task (always listed), or the chain's end.
func (s *Scheduler) links(o *overlay) map[treeNode]*orderedSet[treeNode] {
	if o.links != nil {
		return o.links
	}
	links := map[treeNode]*orderedSet[treeNode]{}
	for _, id := range o.order {
		rec := o.tasks[id]
		if _, live := s.live.get(id); live {
			continue
		}
		if _, settled := s.settled[id]; settled {
			continue
		}
		child := taskTreeNode(id)
		parent, hasParent := parentTreeNode(nodeOf(rec)), true
		for hasParent && (links[parent] == nil || !links[parent].Has(child)) {
			addToSet(links, parent, child)
			if !parent.conversation {
				if _, live := s.live.get(parent.id); live {
					break
				}
				if _, staged := o.tasks[parent.id]; staged {
					break
				}
			}
			var above treeNode
			hasAbove := false
			if parent.conversation {
				if edge, known := s.edge(parent.id, o); known && edge != 0 {
					above, hasAbove = taskTreeNode(edge), true
				}
			} else if fields, ok := s.settled[parent.id]; ok {
				above, hasAbove = parentTreeNode(fields), true
			}
			if hasAbove && s.below[above] != nil && s.below[above].Has(parent) {
				break
			}
			child, parent, hasParent = parent, above, hasAbove
		}
	}
	o.links = links
	return links
}

// ownedLiveOf returns the live ordinary owned work of owner (spec §5.5): the live non-background tasks below it that a
// walk down reaches without entering a background task; with first, only the first. Candidates in o replace committed
// records; the walk does not enter tasks in done, which have none. Owner chains must be loaded.
func (s *Scheduler) ownedLiveOf(owner ID, o *overlay, first bool, done map[ID]bool) []ID {
	opts := walkOptions{o: o}
	if done != nil {
		opts.enter = func(id ID) bool { return !done[id] }
	}
	var found []ID
	s.walkDown(taskTreeNode(owner), opts, func(id ID, n taskNode, rec *jv.Object) bool {
		if rec == nil || n.background {
			return true
		}
		found = append(found, id)
		return !first
	})
	return found
}

func (s *Scheduler) hasOwnedLive(owner ID, o *overlay, done map[ID]bool) bool {
	return len(s.ownedLiveOf(owner, o, true, done)) > 0
}

// known reports whether the chain above node is loaded, without walking: known tasks outside unloaded have a loaded
// chain.
func (s *Scheduler) known(node treeNode) bool {
	for {
		if !node.conversation {
			_, live := s.live.get(node.id)
			_, settled := s.settled[node.id]
			return (live || settled) && !s.unloaded.Has(node.id)
		}
		owner, ok := s.edges[node.id]
		if !ok {
			return false
		}
		if owner == 0 {
			return true
		}
		node = taskTreeNode(owner)
	}
}

// nearestLiveAbove returns the nearest live task above ended ordinary work n, in the overlay's view: the one that work
// held, if any. Work at or below a background task holds nothing above it.
func (s *Scheduler) nearestLiveAbove(n taskNode, o *overlay) *jv.Object {
	for current := n; !current.background; {
		owner := current.owner
		if owner == 0 {
			edge, known := s.edge(current.conversationID, o)
			if !known {
				return nil
			}
			owner = edge
		}
		if owner == 0 {
			return nil
		}
		var rec *jv.Object
		found := false
		if o != nil {
			rec, found = o.tasks[owner]
		}
		if !found {
			rec, found = s.live.get(owner)
		}
		if found && taskStatus(rec) != StatusTerminal {
			return rec
		}
		if found {
			current = nodeOf(rec)
			continue
		}
		above, ok := s.settled[owner]
		if !ok {
			return nil
		}
		current = above
	}
	return nil
}

// release notes that the nearest live task above ended ordinary work may be free now: one holding its outcome, or an
// aborting one.
func (s *Scheduler) release(n taskNode, o *overlay) {
	above := s.nearestLiveAbove(n, o)
	if above == nil {
		return
	}
	if taskStatus(above) == StatusCompleting {
		s.finalizeChecks.Add(mustInt(above, "id"))
	} else if getBool(above, "abortRequested") {
		s.runnable.Add(mustInt(above, "id"))
	}
}

// settle keeps a terminal task a chain load read, so walks pass through it; its own chain is not loaded yet.
func (s *Scheduler) settle(id ID, n taskNode) {
	s.settled[id] = n
	s.unloaded.Add(id)
	s.relist(taskTreeNode(id))
	s.dropIfUnneeded(taskTreeNode(id))
}

// dropIfUnneeded queues an ended task or a conversation for the sweep when nothing is listed below it.
func (s *Scheduler) dropIfUnneeded(n treeNode) {
	if !n.conversation {
		if _, live := s.live.get(n.id); live {
			return
		}
	}
	if _, listed := s.below[n]; listed {
		return
	}
	s.dropQueue.Add(n)
	s.scheduleSweep()
}

// scheduleSweep sweeps in a job of its own on the Session line, after the operation underway: a rejected commit, one
// with nothing to write, and a read publish nothing, so the sweep at the end of observe would not run for what they
// loaded. Pi's #scheduleSweep (scheduler.ts:762-773) queues it with `session.readOnLine(async () => ...)` (session.ts:171-181):
// a read-only line job that awaits nothing, the hopcost.ReadOnLine of an empty body. Measured on Pi ea448f45 (F-ticks-node,
// #enqueue stamped): the sweep starts 5 passes before the next job does, as the core's hold of 4 and its start tick.
func (s *Scheduler) scheduleSweep() {
	if s.sweepScheduled || s.closing {
		return
	}
	s.sweepScheduled = true
	s.loop.ReadLine(func() error {
		s.sweepScheduled = false
		s.sweep()
		return nil
	}, nil)
}

// sweep drops the queued nodes that are still unneeded: not live, nothing listed below. It runs once a publication is
// fully tracked, and as a job of its own on the line, never inside an operation: work a commit creates below a chain its
// own callback loaded keeps that chain. A flat loop: unlisting already queued every node on the way up that lost its last
// listed node.
func (s *Scheduler) sweep() {
	s.dropQueue.Range(func(n treeNode) bool {
		if _, listed := s.below[n]; listed {
			return true
		}
		if n.conversation {
			delete(s.edges, n.id)
			s.roots.Delete(n.id)
		} else if _, live := s.live.get(n.id); !live {
			delete(s.settled, n.id)
			s.unloaded.Delete(n.id)
		}
		return true
	})
	s.dropQueue.Clear()
}

// track brings the scheduler's sets in line with a task's committed record. A task that ended may free the nearest live
// task above it: one holding its outcome finalizes, an abort-marked one runs its abort handler; its waiters may run.
func (s *Scheduler) track(prev *jv.Object, rec *jv.Object) {
	id := mustInt(rec, "id")
	if prev != nil && taskStatus(prev) == StatusWaiting {
		for _, on := range idsOf(getObj(prev, "state"), "on") {
			deleteFromSet(s.waiters, on, id)
		}
	}
	if taskStatus(rec) == StatusTerminal {
		n := nodeOf(rec)
		s.live.delete(id)
		s.sleeping.Delete(id)
		s.intent.Delete(id)
		s.runnable.Delete(id)
		s.finalizeChecks.Delete(id)
		s.settled[id] = n
		// Before anything is forgotten: the walk up to the task it held passes the ended tasks between them. Its chain is
		// loaded, as every commit that ends a task loads the chains of unloaded first.
		s.release(n, nil)
		if waiting := s.waiters[id]; waiting != nil {
			waiting.Range(func(waiter ID) bool {
				s.runnable.Add(waiter)
				return true
			})
		}
		s.relist(taskTreeNode(id))
		s.dropIfUnneeded(taskTreeNode(id))
		return
	}
	s.live.set(id, rec)
	if prev == nil {
		if !s.known(parentTreeNode(nodeOf(rec))) {
			s.unloaded.Add(id)
		}
		s.relist(taskTreeNode(id))
	}
	if cancellationIntent(rec) {
		s.intent.Add(id)
	} else {
		s.intent.Delete(id)
	}
	if taskStatus(rec) == StatusCompleting {
		s.runnable.Delete(id)
		s.finalizeChecks.Add(id)
	} else {
		s.runnable.Add(id)
	}
	if taskStatus(rec) == StatusWaiting {
		for _, on := range idsOf(getObj(rec, "state"), "on") {
			addToSet(s.waiters, on, id)
		}
	}
	if taskStatus(rec) == StatusSleeping {
		s.sleeping.Add(id)
	} else {
		s.sleeping.Delete(id)
	}
}

// relist lists start under its parent node in below while it is a live task or has nodes listed below it, and unlists it
// otherwise; then its parent, as far up as that changes. So below holds exactly the tree around live work; an ended
// parent that loses its last listed node may be forgotten. A loop, as ownership chains can be deeper than the call
// stack.
func (s *Scheduler) relist(start treeNode) {
	node := start
	for {
		needed := s.below[node] != nil
		if !node.conversation {
			if _, live := s.live.get(node.id); live {
				needed = true
			}
		}
		var parent treeNode
		if node.conversation {
			edge, known := s.edges[node.id]
			if !known {
				return
			}
			if edge == 0 {
				if needed {
					s.roots.Add(node.id)
				} else {
					s.roots.Delete(node.id)
				}
				return
			}
			parent = taskTreeNode(edge)
		} else {
			var fields taskNode
			if rec, ok := s.live.get(node.id); ok {
				fields = nodeOf(rec)
			} else if settled, ok := s.settled[node.id]; ok {
				fields = settled
			} else {
				return
			}
			parent = parentTreeNode(fields)
		}
		listed := s.below[parent] != nil && s.below[parent].Has(node)
		if listed == needed {
			return
		}
		if needed {
			first := s.below[parent] == nil
			addToSet(s.below, parent, node)
			s.belowEntries++
			if !first {
				return
			}
		} else {
			deleteFromSet(s.below, parent, node)
			s.belowEntries--
			if s.below[parent] != nil {
				return
			}
			s.dropIfUnneeded(parent)
		}
		node = parent
	}
}
