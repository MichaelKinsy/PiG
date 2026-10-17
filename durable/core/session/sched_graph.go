package session

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// overlayOf is scheduler.ts overlayOf: the tasks and conversation edges a
// transaction has staged.
func overlayOf(tx *Tx) *overlay {
	o := &overlay{tasks: map[ID]*jv.Object{}, edges: map[ID]ID{}}
	for _, rec := range tx.StagedTasks() {
		id := mustInt(rec, "id")
		if _, ok := o.tasks[id]; !ok {
			o.order = append(o.order, id)
		}
		o.tasks[id] = rec
	}
	for _, rec := range tx.StagedConversations() {
		var owner ID
		if ow := getObj(rec, "owner"); ow != nil {
			owner = mustInt(ow, "taskId")
		}
		o.edges[mustInt(rec, "id")] = owner
	}
	return o
}

func (s *Scheduler) setEdge(conv, owner ID) {
	s.edges[conv] = owner
	// The conversation now hangs from its owner, or is a root; with nothing below it, it waits for the sweep.
	s.relist(conversationTreeNode(conv))
	s.dropIfUnneeded(conversationTreeNode(conv))
}

func (s *Scheduler) edge(conv ID, o *overlay) (owner ID, known bool) {
	if o != nil {
		if v, ok := o.edges[conv]; ok {
			return v, true
		}
	}
	v, ok := s.edges[conv]
	return v, ok
}

func (s *Scheduler) node(id ID, o *overlay) (taskNode, bool) {
	if o != nil {
		if rec, ok := o.tasks[id]; ok {
			return nodeOf(rec), true
		}
	}
	if rec, ok := s.live.get(id); ok {
		return nodeOf(rec), true
	}
	n, ok := s.settled[id]
	return n, ok
}

// above walks the ownership chain from start toward the root until fn returns
// false or the chain ends; an unknown link yields a stepUnknown and ends the walk.
func (s *Scheduler) above(start up, o *overlay, fn func(step) bool) {
	at := start
	for {
		if at.isTask {
			n, ok := s.node(at.id, o)
			if !ok {
				fn(step{kind: stepUnknown})
				return
			}
			if !fn(step{kind: stepTask, id: at.id, node: n}) {
				return
			}
			at = parentOf(n)
			continue
		}
		if !fn(step{kind: stepConversation, id: at.id}) {
			return
		}
		owner, ok := s.edge(at.id, o)
		if !ok {
			fn(step{kind: stepUnknown})
			return
		}
		if owner == 0 {
			return
		}
		at = up{isTask: true, id: owner}
	}
}

// inScope reports whether a chain reaches the scope, crossing no background
// task unless crossBackground; unknown when a link is not loaded.
func (s *Scheduler) inScope(start up, sc scope, crossBackground bool) tri {
	result := triUnknown
	decided := false
	s.above(start, nil, func(st step) bool {
		switch st.kind {
		case stepUnknown:
			result, decided = triUnknown, true
			return false
		case stepConversation:
			if !sc.roots && st.id == sc.conversation {
				result, decided = triTrue, true
				return false
			}
		case stepTask:
			if st.node.background && !crossBackground {
				result, decided = triFalse, true
				return false
			}
		}
		return true
	})
	if decided {
		return result
	}
	if sc.roots {
		return triTrue
	}
	return triFalse
}

func (s *Scheduler) belowCancelled(start up) bool {
	// Nothing cancels, the common case: no walk.
	if s.intent.Len() == 0 {
		return false
	}
	result := false
	s.above(start, nil, func(st step) bool {
		switch st.kind {
		case stepUnknown:
			return false
		case stepTask:
			if live, ok := s.live.get(st.id); ok && cancellationIntent(live) {
				result = true
				return false
			}
			if st.node.background {
				return false
			}
		}
		return true
	})
	return result
}

// loadChain loads the links above start that the walk lacks, from the store.
func (s *Scheduler) loadChain(start up, o *overlay) error {
	var passed []ID
	at := start
	for {
		if at.isTask {
			n, ok := s.node(at.id, nil)
			if !ok {
				// Created in this commit: the load from its own record covers what is above it.
				if o != nil {
					if _, created := o.tasks[at.id]; created {
						break
					}
				}
			} else if !s.unloaded.Has(at.id) {
				break
			}
			if !ok {
				s.gateRead("task", at.id)
				rec, err := s.sess.store.Task(at.id)
				if err != nil {
					return err
				}
				if rec == nil {
					return nil
				}
				n = nodeOf(rec)
				if taskStatus(rec) == StatusTerminal {
					s.settle(mustInt(rec, "id"), n)
				}
				// A loaded node can reveal a cancelling owner: the next reconcile derives marks again.
				s.scheduleReconcileAfterRead()
			}
			passed = append(passed, at.id)
			at = parentOf(n)
			continue
		}
		owner, ok := s.edge(at.id, o)
		if !ok {
			s.gateRead("conversation", at.id)
			rec, err := s.sess.store.Conversation(at.id)
			if err != nil {
				return err
			}
			owner = 0
			if rec != nil {
				if ow := getObj(rec, "owner"); ow != nil {
					owner = mustInt(ow, "taskId")
				}
			}
			s.setEdge(at.id, owner)
			// A loaded node can reveal a cancelling owner: the next reconcile derives marks again.
			s.scheduleReconcileAfterRead()
		}
		if owner == 0 {
			break
		}
		at = up{isTask: true, id: owner}
	}
	for _, id := range passed {
		s.unloaded.Delete(id)
	}
	return nil
}

// loadScopes loads the chain of every live task and, with queued, of every
// conversation that has a queued submission, returning those conversations.
func (s *Scheduler) loadScopes(queued bool) ([]ID, error) {
	convs, _, err := s.loadScopesAwaiting(queued)
	return convs, err
}

// loadScopesAwaiting is loadScopes that also returns how many async calls Pi's #loadScopes awaits (scheduler.ts:680-693):
// one `await this.#loadChain(...)` per task still unloaded when its turn comes and per queued-input conversation, and with
// queued the `await scanAll(...)` (util.ts:46-55). Each resumes its caller one tick after the call settles, whether or not
// the call read Storage; the reads themselves are charged as they are made.
func (s *Scheduler) loadScopesAwaiting(queued bool) (convs []ID, awaits int, err error) {
	for _, id := range s.unloaded.Values() {
		// An earlier load in this loop may have passed it.
		if s.unloaded.Has(id) {
			awaits++
			if err := s.loadChain(up{isTask: true, id: id}, nil); err != nil {
				return nil, awaits, err
			}
		}
	}
	if !queued {
		return nil, awaits, nil
	}
	awaits++
	var after ID
	for {
		page, err := s.sess.store.ScanSubmissions(SubmissionQuery{Status: "queued"}, schedScanPage, after)
		if err != nil {
			return nil, awaits, err
		}
		for _, sub := range page.Items {
			if typ, _ := getStr(sub, "type"); typ != "input" {
				continue
			}
			if c := mustInt(sub, "conversationId"); !slices.Contains(convs, c) {
				convs = append(convs, c)
			}
		}
		if page.After == 0 {
			break
		}
		after = page.After
	}
	for _, c := range convs {
		awaits++
		if err := s.loadChain(up{id: c}, nil); err != nil {
			return nil, awaits, err
		}
	}
	return convs, awaits, nil
}

// gateRead lets the host's Storage see a read the chain load makes.
func (s *Scheduler) gateRead(method string, id ID) {
	if s.env.GateRead != nil {
		s.env.GateRead(method, id)
	}
}
