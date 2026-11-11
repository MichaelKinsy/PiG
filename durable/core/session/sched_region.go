package session

import (
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// Decision regions (scheduler.ts #region, #record, #liveRecord, membershipKeys, #chainKeys, #pin). The scheduler's memory changes only as
// each commit is published, while the line runs on and stores the commits of several jobs in one call. A decision that reads it therefore
// records the keys it reads (beginRegion, the helpers' record calls, endRegion): when one has an unpublished writer, the job waits for its
// publication and runs again from its start (*NeedPublication), and only a decision that read nothing unpublished stands. Each region
// costs the one tick of Pi's `await this.#region(...)`.

// beginRegion starts recording the keys the decision reads.
func (s *Scheduler) beginRegion() {
	s.reading = true
	s.regionKeys = s.regionKeys[:0]
	clear(s.regionSeen)
}

// endRegion ends the region the transaction's job decided in (tx nil: a read job's): nil when none of the keys it read has an unpublished writer (the region costs
// its tick), else a *NeedPublication for the newest of them, which makes the job run again once it is published.
func (s *Scheduler) endRegion(tx *Tx) error { return s.closeRegion(tx, 1) }

// endRegionPlain is endRegion of a region that has no `refresh`: once the gates open, `await refresh?.()` of undefined costs one tick more
// before the decision runs again.
func (s *Scheduler) endRegionPlain(tx *Tx) error { return s.closeRegion(tx, 2) }

func (s *Scheduler) closeRegion(tx *Tx, gateExtra int) error {
	s.reading = false
	if tx != nil {
		s.sess.touchAt = tx.ticks
	} else {
		s.sess.touchAt = max(s.sess.lastTicks, 0)
	}
	var newest *Gate
	var at *Part
	for _, key := range s.regionKeys {
		var p *Part
		if key == AllKeys {
			if s.sess.writer.newest != nil {
				p = s.sess.writer.newest
			}
		} else {
			p = s.sess.writer.unpublished[key]
		}
		if p != nil && (at == nil || p.Index >= at.Index) {
			at, newest = p, &p.gate
		}
	}
	s.trimRegion()
	if newest != nil {
		// Promise.all of the gates adds a tick to the wait.
		return s.sess.needPublicationAfter(newest, gateExtra)
	}
	if tx != nil {
		tx.charge(1)
	} else {
		s.sess.ChargeRead(1)
	}
	return nil
}

// regionKeep is the number of keys of one region past which its record is let go when the region ends.
const regionKeep = 1024

// trimRegion lets go of the record of a region that read thousands of keys, which a map and a slice would keep at their largest size.
func (s *Scheduler) trimRegion() {
	if len(s.regionKeys) > regionKeep {
		s.regionKeys, s.regionSeen = nil, nil
	}
}

// record notes, in a decision region, that it reads key; anywhere else, nothing.
func (s *Scheduler) record(key string) {
	if !s.reading {
		return
	}
	if _, seen := s.regionSeen[key]; seen {
		return
	}
	if s.regionSeen == nil {
		s.regionSeen = map[string]struct{}{}
	}
	s.regionSeen[key] = struct{}{}
	s.regionKeys = append(s.regionKeys, key)
}

// recordOwned notes the read of the live work listed below a node of the ownership tree.
func (s *Scheduler) recordOwned(n treeNode) {
	if !s.reading {
		return
	}
	s.record(OwnedKey(nodeKey(n)))
}

// nodeKey is a node of the ownership tree as OwnedKey names it: a task ID, or "c" and a conversation ID.
func nodeKey(n treeNode) string {
	if n.conversation {
		return "c" + strconv.FormatInt(n.id, 10)
	}
	return strconv.FormatInt(n.id, 10)
}

// liveRecord is a live task's committed record, read by a decision region.
func (s *Scheduler) liveRecord(id ID) (*jv.Object, bool) {
	if s.reading {
		s.record(TaskKey(id))
	}
	return s.live.get(id)
}

// pin keeps n, which a chain load of the job on the line installed, until that job's commit is published or fails: work it creates below the
// node needs it, and the sweep would drop it as unneeded before then.
func (s *Scheduler) pin(n treeNode) {
	if s.pins == nil {
		s.pins = map[treeNode]int{}
	}
	s.pins[n]++
	s.loop.OnJobEnd(&pinRelease{s: s, n: n})
}

// pinRelease is the end of a pin: the node goes once the last pin of it ends.
type pinRelease struct {
	s *Scheduler
	n treeNode
}

func (p *pinRelease) Resume() {
	s := p.s
	if s.pins[p.n]--; s.pins[p.n] > 0 {
		return
	}
	delete(s.pins, p.n)
	s.dropIfUnneeded(p.n)
}

// intentChanges reports whether replacing previous (nil: a new task) with next changes the cancellation intent set or what it means: a mark
// set, upgraded or cleared, or a held or ended outcome other than completed.
func intentChanges(previous, next *jv.Object) bool {
	if previous == nil {
		return getBool(next, "abortRequested") || next.Has("abortReason") || failedOutcome(next) || cancellationIntent(next)
	}
	return getBool(previous, "abortRequested") != getBool(next, "abortRequested") ||
		abortReasonOf(previous) != abortReasonOf(next) ||
		failedOutcome(previous) != failedOutcome(next) ||
		cancellationIntent(previous) != cancellationIntent(next)
}

// membershipKeys is scheduler.ts membershipKeys: the ownership memberships a commit's task and conversation changes write, at its admission on
// the line: for each task created or made terminal, every node from its parent up to and including the nearest live task, or the root
// conversation, and then the roots too, conservatively, as a pending end may unlist the root a creation finds listed; for each conversation
// created under a task, that task; and the intent set when a change sets, upgrades or clears a mark or a held or ended outcome other than
// completed. It loads the chains it walks; the nodes it installs stay until the commit is published. Its awaits are the commit's: one tick for
// the async call, and two for each chain it loads and walks.
func (s *Scheduler) membershipKeys(tx *Tx) ([]string, error) {
	tasks := tx.StagedTasks()
	convs := tx.StagedConversations()
	owned := false
	for _, c := range convs {
		if getObj(c, "owner") != nil {
			owned = true
		}
	}
	if len(tasks) == 0 && !owned {
		return nil, nil
	}
	var keys []string
	add := func(k string) {
		if slices.Contains(keys, k) {
			return
		}
		keys = append(keys, k)
	}
	o := overlayOf(tx)
	created := map[ID]bool{}
	for _, id := range tx.createdTasks() {
		created[id] = true
	}
	reached := map[string]ID{}
	ticks := 1
	for _, rec := range tasks {
		id := mustInt(rec, "id")
		prev, _ := s.live.get(id)
		if intentChanges(prev, rec) {
			add(IntentKey)
		}
		if !created[id] && taskStatus(rec) != StatusTerminal {
			continue
		}
		ticks += 2
		s.loadTicks = 0
		if err := s.loadChain(parentOf(nodeOf(rec)), o); err != nil {
			return nil, err
		}
		ticks += s.loadTicks
		root, err := s.chainKeys(parentTreeNode(nodeOf(rec)), o, add, reached)
		if err != nil {
			return nil, err
		}
		if root != 0 {
			add(RootsKey)
		}
	}
	for _, c := range convs {
		ow := getObj(c, "owner")
		if ow == nil {
			continue
		}
		owner := mustInt(ow, "taskId")
		ticks += 2
		s.loadTicks = 0
		if err := s.loadChain(up{isTask: true, id: owner}, o); err != nil {
			return nil, err
		}
		ticks += s.loadTicks
		if _, err := s.chainKeys(taskTreeNode(owner), o, add, reached); err != nil {
			return nil, err
		}
	}
	s.sess.job.Membership += ticks
	return keys, nil
}

// chainKeys adds the memberships from start up to and including the nearest live task, or the root conversation, which it returns (0 when the
// chain ends elsewhere). reached keeps, per node walked, where its walk ended, so a chain several tasks of a commit share is walked once. Whether
// an ancestor is live can change under a pending commit, so it is read after a touch of the ancestor's record: a pending end must not stop the
// chain early. Ownership fields are immutable, so not keyed.
func (s *Scheduler) chainKeys(start treeNode, o *overlay, add func(string), reached map[string]ID) (ID, error) {
	var walked []string
	var root ID
	node := start
	for {
		key := OwnedKey(nodeKey(node))
		if r, seen := reached[key]; seen {
			root = r
			break
		}
		add(key)
		walked = append(walked, key)
		if node.conversation {
			owner, known := s.edge(node.id, o)
			if !known {
				break
			}
			if owner == 0 {
				root = node.id
				break
			}
			node = taskTreeNode(owner)
			continue
		}
		rec, staged := o.tasks[node.id]
		if !staged {
			if err := s.sess.Touch(TaskKey(node.id)); err != nil {
				return 0, err
			}
			rec, _ = s.live.get(node.id)
		}
		if rec != nil && taskStatus(rec) != StatusTerminal {
			break
		}
		var above taskNode
		if rec != nil {
			above = nodeOf(rec)
		} else if settled, ok := s.settled[node.id]; ok {
			above = settled
		} else {
			break
		}
		node = parentTreeNode(above)
	}
	for _, k := range walked {
		reached[k] = root
	}
	return root, nil
}

// published is `await this.#published()`: on the line, wait until every commit admitted before is published, for a decision that reads what cannot
// be keyed (every live task: open, inspection, or the tasks a conversation abort reaches). The job runs again once they are; the await of the
// settled promise of the async function costs its caller one tick, charged to the transaction `tx` of the commit callback that awaits it, or
// to the read job when `tx` is nil.
func (s *Scheduler) published(tx *Tx) error {
	if err := s.sess.Touch(AllKeys); err != nil {
		return err
	}
	if tx != nil {
		tx.Await(1)
	} else {
		s.sess.ChargeRead(1)
	}
	return nil
}
