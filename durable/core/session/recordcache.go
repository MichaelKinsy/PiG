// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import (
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
)

// Which table records the Session's CachedStorage holds (session/cached-storage.ts), so a read of one costs a cached answer's ticks
// (hopcost.RecordRead) and not a Storage read's. The records themselves are the Store's: the core reads them from it at once and keeps only
// the membership: conversations and submissions in a bounded recency list, live tasks until they end, the ended tasks a waiting task names
// until it moved on, and the other ended tasks in a bounded list. A read-only Session shows a Storage another process writes: it keeps nothing.

// The bounds of the cached tables (cached-storage.ts: the Lru capacities).
const (
	conversationCapacity = 4096
	endedTaskCapacity    = 256
	submissionCapacity   = 256
)

// awaitedTask is a task some task waits on: how many waits name it, and whether its ended record is kept.
type awaitedTask struct {
	count int
	ended bool
}

// taskWait is a task that waits, or left its wait and has not changed since: what it waited on, and whether it left.
type taskWait struct {
	on   []ID
	left bool
}

type recordCache struct {
	conversations lruMap
	ended         lruMap
	submissions   lruMap
	live          map[ID]struct{}
	awaited       map[ID]*awaitedTask
	waits         map[ID]*taskWait
}

func (c *recordCache) init() {
	c.conversations.capacity = conversationCapacity
	c.ended.capacity = endedTaskCapacity
	c.submissions.capacity = submissionCapacity
}

// remove drops key from the list.
func (m *lruMap) remove(key ID) {
	if n := m.nodes[key]; n != nil {
		m.unlink(n)
		delete(m.nodes, key)
	}
}

// answer is how the cache answers a read: a hit, a miss, or not at all for a read-only Session.
func (s *Session) answer(has bool) hopcost.Cache {
	switch {
	case s.ReadOnly:
		return hopcost.CacheOff
	case has:
		return hopcost.CacheHit
	}
	return hopcost.CacheMiss
}

// hasConversation is how the cache answers a read of the conversation; a hit makes it the most recently used.
func (s *Session) hasConversation(id ID) hopcost.Cache {
	return s.answer(!s.ReadOnly && s.records.conversations.use(id) != nil)
}

// hasTask is how the cache answers a read of the task: a live one, an ended one a wait keeps, or one in the bounded list, is a hit.
func (s *Session) hasTask(id ID) hopcost.Cache {
	if s.ReadOnly {
		return hopcost.CacheOff
	}
	c := &s.records
	if _, ok := c.live[id]; ok {
		return hopcost.CacheHit
	}
	if a := c.awaited[id]; a != nil && a.ended {
		return hopcost.CacheHit
	}
	return s.answer(c.ended.use(id) != nil)
}

// hasSubmission is how the cache answers a read of the submission: one this Session wrote and kept is a hit.
func (s *Session) hasSubmission(id ID) hopcost.Cache {
	return s.answer(!s.ReadOnly && s.records.submissions.use(id) != nil)
}

// keepConversation keeps a conversation a read found.
func (s *Session) keepConversation(id ID) {
	if !s.ReadOnly {
		s.records.conversations.put(id)
	}
}

// keepTask keeps a task record a read found, or a commit wrote (cached-storage.ts #keepTask).
func (s *Session) keepTask(record *jv.Object) {
	if s.ReadOnly {
		return
	}
	c := &s.records
	id := mustInt(record, "id")
	if taskStatus(record) == StatusTerminal {
		delete(c.live, id)
		if a := c.awaited[id]; a != nil {
			a.ended = true
		} else {
			c.ended.put(id)
		}
		return
	}
	c.ended.remove(id)
	if c.live == nil {
		c.live = map[ID]struct{}{}
	}
	c.live[id] = struct{}{}
}

// followWait follows a task's waits from its written record: a wait holds the ended records of its `on` until the task, after leaving the
// wait, changes again (or ends), as the phase that resumes reads their outcomes before its next commit (cached-storage.ts #followWait).
func (c *recordCache) followWait(record *jv.Object) {
	id := mustInt(record, "id")
	wait := c.waits[id]
	status := taskStatus(record)
	if status == StatusWaiting {
		if wait != nil {
			c.release(wait.on)
		}
		on := idsOf(getObj(record, "state"), "on")
		if c.waits == nil {
			c.waits = map[ID]*taskWait{}
		}
		c.waits[id] = &taskWait{on: on}
		for _, on := range on {
			a := c.awaited[on]
			if a == nil {
				a = &awaitedTask{}
				if c.ended.nodes[on] != nil {
					c.ended.remove(on)
					a.ended = true
				}
				if c.awaited == nil {
					c.awaited = map[ID]*awaitedTask{}
				}
				c.awaited[on] = a
			}
			a.count++
		}
		return
	}
	if wait == nil {
		return
	}
	if !wait.left && status != StatusTerminal {
		wait.left = true
		return
	}
	delete(c.waits, id)
	c.release(wait.on)
}

// release ends a wait on `on`: what no other wait names goes back to the bounded list.
func (c *recordCache) release(on []ID) {
	for _, id := range on {
		a := c.awaited[id]
		if a == nil {
			continue
		}
		if a.count--; a.count > 0 {
			continue
		}
		delete(c.awaited, id)
		if a.ended {
			c.ended.put(id)
		}
	}
}

// applyRecords applies a stored commit when it is published (cached-storage.ts published()).
func (s *Session) applyRecords(changes []Change) {
	if s.ReadOnly {
		return
	}
	for i := range changes {
		c := &changes[i]
		if c.Kind != ChangeTable {
			continue
		}
		switch c.Table {
		case WriteConversation:
			s.records.conversations.put(mustInt(c.Record, "id"))
		case WriteTask:
			s.records.followWait(c.Record)
			s.keepTask(c.Record)
		case WriteSubmission:
			s.records.submissions.put(mustInt(c.Record, "id"))
		}
	}
}

// RecordAnswer is how the cache answers a client read of one record (storage.conversation(id), task(id), submission(id) on the line).
func (s *Session) RecordAnswer(r hopcost.Record, id ID) hopcost.Cache {
	switch r {
	case hopcost.RecordConversation:
		return s.hasConversation(id)
	case hopcost.RecordTask:
		return s.hasTask(id)
	}
	return s.hasSubmission(id)
}

// KeepRecord keeps the record a read that missed found (cached-storage.ts #keep): a conversation or a task; a submission read is passed
// through, kept only as a commit writes it.
func (s *Session) KeepRecord(r hopcost.Record, id ID, rec *jv.Object) {
	switch {
	case rec == nil:
	case r == hopcost.RecordConversation:
		s.keepConversation(id)
	case r == hopcost.RecordTask:
		s.keepTask(rec)
	}
}
