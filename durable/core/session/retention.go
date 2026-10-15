// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import "math"

// Ports packages/durable/src/session/session.ts document retention (#touchDocument, #scheduleDocumentSweep, #sweepDocuments,
// documentRetention). The cache holds no observer here, so no incarnation is pinned. The core has no timer and no job of its own
// to run later: the sweep a timer starts is the end of the next commit, which is where Pi's variant without unreferenced timers
// evicts too. Every clock read Pi makes for the bookkeeping is made, in the same order, because the clock is shared with task
// times.

// DefaultDocumentRetentionMs is Pi's default document retention: ten minutes, as for kept contexts.
const DefaultDocumentRetentionMs = 600_000

// idleDoc is one cached document with no observer: its address, the time of its last use, and its neighbours in use order.
type idleDoc struct {
	id         string
	since      float64
	prev, next *idleDoc
}

// documentRetention is the retention in effect: the Session's setting when it is a number of milliseconds, 0 or more
// (Infinity keeps documents); otherwise the default.
func (s *Session) documentRetention() float64 {
	if s.DocumentRetention != nil {
		if ms := s.DocumentRetention(); ms != nil && *ms >= 0 {
			return *ms
		}
	}
	return DefaultDocumentRetentionMs
}

// touchDocument notes a use of a cached document: it becomes the newest idle one, and an eviction is scheduled.
func (s *Session) touchDocument(id string) {
	cached := s.docs[id]
	if cached == nil || s.attached[mustInt(cached.record, "id")] > 0 {
		return
	}
	now := s.now()
	since := s.idle[id]
	// A hot document moves to the back at most once per granule, a tenth of the retention up to 1 s.
	if since != nil && now-since.since < math.Min(1000, s.documentRetention()/10) {
		return
	}
	s.unlinkIdle(id)
	if s.idle == nil {
		s.idle = map[string]*idleDoc{}
	}
	node := &idleDoc{id: id, since: now, prev: s.idleTail}
	if s.idleTail != nil {
		s.idleTail.next = node
	} else {
		s.idleHead = node
	}
	s.idleTail = node
	s.idle[id] = node
	// A pending sweep for the oldest document comes first unless the retention was shortened since; then it moves up.
	if !s.sweepPending || s.sweepAt > now+s.documentRetention() {
		s.scheduleDocumentSweep()
	}
}

// unlinkIdle removes a document from the idle order.
func (s *Session) unlinkIdle(id string) {
	node := s.idle[id]
	if node == nil {
		return
	}
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		s.idleHead = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	} else {
		s.idleTail = node.prev
	}
	delete(s.idle, id)
}

// dropDocument takes a document out of the cache and the idle order.
func (s *Session) dropDocument(id string) {
	delete(s.docs, id)
	s.unlinkIdle(id)
}

// scheduleDocumentSweep schedules the eviction of the oldest idle document: on the line at once when it is due (retention 0),
// otherwise by a host timer when it expires (session.ts #scheduleDocumentSweep). Without the hooks the sweep stays pending and the
// end of each commit evicts what expired.
func (s *Session) scheduleDocumentSweep() {
	if s.closed {
		return
	}
	oldest := s.idleHead
	if oldest == nil {
		return
	}
	at := oldest.since + s.documentRetention()
	if s.sweepPending && s.sweepAt <= at {
		return
	}
	if s.sweepCancel != nil {
		s.sweepCancel()
		s.sweepCancel = nil
	}
	// The pending sweep stays set until its job runs, so touches while the line is busy queue no more.
	s.sweepToken++
	token := s.sweepToken
	run := func() {
		s.SweepJobs++
		if s.EnqueueSweep == nil {
			return
		}
		s.EnqueueSweep(func() {
			if s.sweepToken == token {
				s.sweepPending, s.sweepCancel = false, nil
			}
			if !s.closed {
				s.sweepDocuments()
			}
		})
	}
	delay := at - s.now()
	s.sweepPending, s.sweepAt = true, at
	if delay <= 0 {
		if s.Microtask != nil {
			s.Microtask(run)
		}
		return
	}
	if s.ArmTimer == nil {
		return
	}
	s.sweepCancel = s.ArmTimer(at, run)
}

// sweepDocuments evicts the idle documents whose retention passed, oldest first, then schedules the next eviction. It runs at
// the end of each commit outcome.
func (s *Session) sweepDocuments() {
	now := s.now()
	expired := now - s.documentRetention()
	if s.hookless() && s.sweepPending && s.sweepAt <= now {
		// No job or timer to run the pending sweep: this one is it.
		s.sweepPending = false
	}
	for node := s.idleHead; node != nil && node.since <= expired; node = s.idleHead {
		s.dropDocument(node.id)
	}
	s.scheduleDocumentSweep()
}

// hookless is a Session with no way to run a sweep later: the end of a commit is the only sweep it has.
func (s *Session) hookless() bool {
	return s.EnqueueSweep == nil || s.ArmTimer == nil || s.Microtask == nil
}

// clearIdle forgets the idle order: the cache was emptied.
func (s *Session) clearIdle() {
	s.idle, s.idleHead, s.idleTail = nil, nil, nil
}

// DocumentCacheSizes is the number of cached documents and how many of them are idle (session.ts documentCacheSizes); the rest are
// pinned by observers, and the core has none.
func (s *Session) DocumentCacheSizes() (cached, idle int) { return len(s.docs), len(s.idle) }

// PinDocument keeps the cached incarnation of a document in the cache for one observer (session.ts #pinDocument): it leaves the
// idle order, and the returned release unpins it once; the incarnation is idle again after the last release, when it is still the
// cached one. It returns nil when the document is not cached.
func (s *Session) PinDocument(def *DocDef, args DocArgs) (release func()) {
	id := addressID(def.address(args))
	cached := s.docs[id]
	if cached == nil {
		return nil
	}
	recordID := mustInt(cached.record, "id")
	if s.attached == nil {
		s.attached = map[ID]int{}
	}
	s.attached[recordID]++
	s.unlinkIdle(id)
	pinned := true
	return func() {
		if !pinned {
			return
		}
		pinned = false
		if s.attached[recordID]--; s.attached[recordID] > 0 {
			return
		}
		delete(s.attached, recordID)
		if cur := s.docs[id]; cur != nil && mustInt(cur.record, "id") == recordID {
			s.touchDocument(id)
		}
	}
}

// DocumentsAttached is the number of incarnations an observer pins (session.ts documentCacheSizes.attached).
func (s *Session) DocumentsAttached() int { return len(s.attached) }
