package session

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// The context range a task runtime read last, per conversation (scheduler.ts #contexts): a later read, by any task of the
// conversation, scans only newer entries. The core derives contexts from its own transcript store, so this keeps only what
// decides which Storage reads Pi makes: the head marker and the tail of the range, and when the conversation first looked
// idle. It is derived, never persisted, dropped at the first idle check after contextRetentionMs of idleness, and at seal.

// ContextRange is the bounds of a kept context range: the entries from From (0: the first) to Tail visible from the conversation,
// under the head marker Head (0: none), whose head is From. A range that is Current reached the conversation's newest entry when
// it was read, so the entries of later commits extend it at their publication (advanceContexts); one cut off at an earlier entry
// is not.
type ContextRange struct {
	Head, From, Tail ID
	Current          bool
}

type keptContext struct {
	ContextRange
	idleSince float64
	// expiresAt is when the idle period that began at idleSince ends, fixed when it began.
	expiresAt float64
	hasIdle   bool
}

func (s *Scheduler) retention() float64 {
	if s.env.ContextRetentionMs == nil {
		return 0
	}
	return s.env.ContextRetentionMs()
}

// KeptContext returns the bounds of the range kept for the conversation.
func (s *Scheduler) KeptContext(conversationID ID) (ContextRange, bool) {
	kept := s.contexts[conversationID]
	if kept == nil {
		return ContextRange{}, false
	}
	return kept.ContextRange, true
}

// KeptEntry reports whether entry id lies within the range kept for the conversation: such an entry is committed and visible from
// it (scheduler.ts runtime.entry reads it from the kept range, rangeEntry).
func (s *Scheduler) KeptEntry(conversationID, id ID) bool {
	kept := s.contexts[conversationID]
	return kept != nil && id >= kept.From && id <= kept.Tail
}

// KeepContext keeps the range a runtime read returned unless the invocation ended, the Harness is closing, or a concurrent
// read already kept a newer range. A read of another, idle conversation starts or continues its retention period.
//
// A range that is Current stays so only if the Session knows its tail as the conversation's newest entry now: a commit published since the
// read returned was not seen by advanceContexts, so a range that missed it must not be advanced.
func (s *Scheduler) KeepContext(conversationID ID, r ContextRange, invocationEnded bool) {
	if s.closing || invocationEnded {
		return
	}
	if newest, known := s.sess.KnownLatestEntryID(conversationID); r.Current && (!known || newest != r.Tail) {
		r.Current = false
	}
	kept := s.contexts[conversationID]
	if kept != nil && kept.Tail > r.Tail {
		return
	}
	if !s.idle(conversationID) {
		s.contexts[conversationID] = &keptContext{ContextRange: r}
		s.idleOrder.Delete(conversationID)
		s.deferIdleIfUnloaded(conversationID)
		return
	}
	if s.retention() > 0 {
		// An idle period that already started keeps its expiry; a new one starts now.
		next := &keptContext{ContextRange: r, idleSince: s.env.Now(), hasIdle: true}
		next.expiresAt = next.idleSince + s.retention()
		if kept != nil && kept.hasIdle {
			next.idleSince, next.expiresAt = kept.idleSince, kept.expiresAt
		}
		s.contexts[conversationID] = next
		// An idle period that already started keeps its place in the expiry order; a new one starts now.
		if kept == nil || !kept.hasIdle {
			s.markIdle(conversationID, next.idleSince)
		}
		s.scheduleExpiry()
	}
}

// advanceContexts extends each kept context range with the entries a stored commit appended to its conversation, so a later read
// needs no Storage scan (scheduler.ts #advanceContexts, context.ts advanceRange). Entries are published in ID order. An idle range
// past its retention is dropped here too: entry-only commits do not run settleIdle, and a host may decline the expiry timer.
func (s *Scheduler) advanceContexts(pub *Publication) {
	if len(s.contexts) == 0 {
		return
	}
	for i := range pub.Changes {
		ch := &pub.Changes[i]
		if ch.Kind != ChangeTable || ch.Table != WriteEntry {
			continue
		}
		conversationID := mustInt(ch.Record, "conversationId")
		kept := s.contexts[conversationID]
		if kept == nil {
			continue
		}
		if kept.hasIdle && kept.expiresAt <= s.env.Now() {
			s.dropContext(conversationID)
			continue
		}
		if !kept.Current {
			continue
		}
		kept.Tail = mustInt(ch.Record, "id")
		if !ch.Record.Has("head") {
			continue
		}
		// Heads only move forward: compaction keeps a later entry, a reset starts at itself, an older head is stale.
		from := mustInt(ch.Record, "head")
		if kept.Head != 0 && from < kept.From {
			s.dropContext(conversationID)
			continue
		}
		kept.Head, kept.From = mustInt(ch.Record, "id"), from
	}
}

// idleScopes is a set of conversations whose idleness a change can change; nil stands for every conversation.
type idleScopes map[ID]struct{}

// addScopesAbove adds the conversations above rec to scopes: those whose idleness a change of it can change. It reports false when
// a node on the way up is not known. A conversation already in scopes has everything above it there too, and so has a task in reached
// (when it is not nil), the tasks of this commit whose way up was walked: a chain of owners is walked once, not once per task. A loop
// rather than s.above with a callback: it runs once per commit.
func (s *Scheduler) addScopesAbove(rec *jv.Object, scopes, reached idleScopes) bool {
	if reached != nil {
		reached[mustInt(rec, "id")] = struct{}{}
	}
	at := parentOf(nodeOf(rec))
	for {
		if at.isTask {
			if reached != nil {
				if _, done := reached[at.id]; done {
					return true
				}
				reached[at.id] = struct{}{}
			}
			n, ok := s.node(at.id, nil)
			if !ok {
				return false
			}
			at = parentOf(n)
			continue
		}
		if _, done := scopes[at.id]; done {
			return true
		}
		scopes[at.id] = struct{}{}
		owner, ok := s.edge(at.id, nil)
		if !ok {
			return false
		}
		if owner == 0 {
			return true
		}
		at = up{isTask: true, id: owner}
	}
}

// effectiveScopes is scopes with the conversations whose idle check was deferred; nil (every conversation) when scopes is nil or a
// check over every scope is owed. It adds to scopes.
func (s *Scheduler) effectiveScopes(scopes idleScopes) idleScopes {
	if scopes == nil || s.unknownsChanged {
		return nil
	}
	for id := range s.idleDeferred {
		scopes[id] = struct{}{}
	}
	return scopes
}

// hasLiveUnloaded reports whether a live task's chain is not loaded yet: one idle counts as inside every scope it may be in. It
// reads the set's slots, as a Range with a callback would allocate its closure on every call.
func (s *Scheduler) hasLiveUnloaded() bool {
	for at := range s.unloaded.slots {
		if item := s.unloaded.item(at); item.live {
			if _, live := s.live.get(item.key); live {
				return true
			}
		}
	}
	return false
}

// deferIdleIfUnloaded leaves the idle check of a conversation judged busy to the load of the chain that made it so, when it was a
// live task whose chain is not loaded: no task change below the conversation will check it.
func (s *Scheduler) deferIdleIfUnloaded(conversationID ID) {
	if s.hasLiveUnloaded() {
		s.idleDeferred[conversationID] = struct{}{}
	}
}

// settleIdle is scheduler.ts #settleIdle after task changes below the conversations in scopes (every conversation when nil): it
// resolves their idle waiters and the Harness-wide ones, drops the kept contexts whose idle period passed the retention, then starts
// or ends the idle periods of the kept contexts in scopes. A task change can change the idleness only of the conversations above it,
// so no check visits every conversation.
//
// Pi 35dfa757 settles the conversations above the changed tasks, and expires idle ranges from an index ordered
// by idleSince.
func (s *Scheduler) settleIdle(scopes idleScopes) {
	now, retention := s.env.Now(), s.retention()
	if s.hasLiveUnloaded() {
		// Every scope counts a live task whose chain is not loaded as busy, so no check of one is trustworthy: it waits for the load
		// (loadChain), whose commit or reconcile pass settles it. Expiry does not depend on idleness.
		if scopes == nil {
			s.unknownsChanged = true
		}
		for id := range scopes {
			s.idleDeferred[id] = struct{}{}
		}
		s.expireContexts(now, retention)
		s.scheduleExpiry()
		return
	}
	scopes = s.effectiveScopes(scopes)
	s.unknownsChanged = false
	clear(s.idleDeferred)
	if scopes == nil {
		s.fullIdleChecks++
	}
	s.resolveIdleWaiters(scopes)
	// Expired first: a context past its retention is dropped even when the same commit makes it busy again.
	s.expireContexts(now, retention)
	ids := s.takeIDs()
	if scopes == nil {
		for id := range s.contexts {
			ids = append(ids, id)
		}
	} else {
		for id := range scopes {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		kept := s.contexts[id]
		if kept == nil {
			continue
		}
		switch {
		case !s.idle(id):
			if kept.hasIdle {
				kept.hasIdle = false
				s.idleOrder.Delete(id)
			}
		case retention <= 0:
			s.dropContext(id)
		case !kept.hasIdle:
			kept.idleSince, kept.expiresAt, kept.hasIdle = now, now+retention, true
			s.markIdle(id, now)
		}
	}
	s.idScratch = ids
	s.scheduleExpiry()
}

// settleAfterReconcile is what the end of a reconcile pass settles. Its commit's task changes were settled at their publication; a
// waiter that asked for this pass checks again, and what its chain loads resolved can make a scope idle. A pass checks expiry too,
// for hosts that decline the expiry timer.
func (s *Scheduler) settleAfterReconcile() {
	if s.unknownsChanged || len(s.idleDeferred) > 0 {
		clear(s.dirtyScopes)
		s.settleIdle(s.dirtyScopes)
	}
	s.resolveIdleWaiters(nil)
	s.expireContexts(s.env.Now(), s.retention())
	s.scheduleExpiry()
}

// resolveIdleWaiters resolves the idle waiters of the conversations in scopes (all when nil) and the Harness-wide ones.
func (s *Scheduler) resolveIdleWaiters(scopes idleScopes) {
	for _, id := range slices.Clone(s.idleWaitKeys) {
		if _, inScopes := scopes[id]; scopes != nil && id != 0 && !inScopes {
			continue
		}
		if !s.idle(id) {
			continue
		}
		list := s.idleWaiters[id]
		s.dropIdleKey(id)
		for _, w := range list {
			if w.remove != nil {
				w.remove()
			}
			s.deliverIdle(w.done, w.late, nil)
		}
	}
}

// expireContexts drops the kept contexts idle for at least retention, oldest first; with no retention, every idle one, also those
// whose idle period a clock that went back makes start after now.
func (s *Scheduler) expireContexts(now, retention float64) {
	for {
		id, ok := s.idleOrder.First()
		if !ok {
			return
		}
		kept := s.contexts[id]
		if kept == nil || !kept.hasIdle {
			s.idleOrder.Delete(id)
			continue
		}
		if retention > 0 && now-kept.idleSince < retention {
			return
		}
		s.dropContext(id)
	}
}

// markIdle starts the idle period of a kept context in the expiry order. An idle period older than the newest one kept, which a clock
// that went back starts, goes where it belongs among the others, which are in order, and the newest is taken from what is kept then.
func (s *Scheduler) markIdle(id ID, idleSince float64) {
	s.idleOrder.Delete(id)
	s.idleOrder.Add(id)
	if s.idleOrder.Len() == 1 || idleSince >= s.idleNewest {
		s.idleNewest = idleSince
		return
	}
	order := s.idleOrder.Values()
	// id is last; the others are sorted: move it before the first that is newer than it, as a stable sort would leave it.
	at := len(order) - 1
	for at > 0 && s.contexts[order[at-1]].idleSince > idleSince {
		order[at] = order[at-1]
		at--
	}
	order[at] = id
	s.idleOrder.Clear()
	for _, each := range order {
		s.idleOrder.Add(each)
	}
	s.idleNewest = s.contexts[order[len(order)-1]].idleSince
}

// dropContext drops the range kept for the conversation, and tells the core it may release what it derived.
func (s *Scheduler) dropContext(id ID) {
	delete(s.contexts, id)
	s.idleOrder.Delete(id)
	if s.env.ReleaseContext != nil {
		s.env.ReleaseContext(id)
	}
}

// scheduleExpiry runs expireIdleContexts when the earliest idle context expires (scheduler.ts #scheduleExpiry).
func (s *Scheduler) scheduleExpiry() {
	retention := s.retention()
	var at float64
	oldest, found := s.idleOrder.First()
	if found {
		at = s.contexts[oldest].idleSince + retention
	}
	if s.expiry.set && found && s.expiry.at == at {
		return
	}
	s.cancelExpiry()
	if !found || s.closing || s.env.Timer == nil {
		return
	}
	s.expiry.set, s.expiry.at = true, at
	s.expiry.cancel = s.env.Timer(at, func() {
		s.expiry.set, s.expiry.cancel = false, nil
		s.expireContexts(s.env.Now(), s.retention())
		s.scheduleExpiry()
	})
}

func (s *Scheduler) cancelExpiry() {
	if s.expiry.set && s.expiry.cancel != nil {
		s.expiry.cancel()
	}
	s.expiry.set, s.expiry.cancel = false, nil
}

// Ended reports whether the invocation ended.
func (r *Runtime) Ended() bool { return r.inv.ended }

// EndedError is the error of an operation of an ended invocation.
func (r *Runtime) EndedError() error { return endedError(r.inv) }
