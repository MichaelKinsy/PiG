// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import (
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
)

// The membership of the Session's record cache follows session/cached-storage.ts: conversations and written submissions in bounded recency
// lists, live tasks until they end, ended tasks in a bounded list unless a waiting task names them.

func conversationWrite(id ID) Change {
	return Change{Kind: ChangeTable, Table: WriteConversation, Record: obj("id", idv(id))}
}

func submissionWrite(id ID) Change {
	return Change{Kind: ChangeTable, Table: WriteSubmission, Record: obj("id", idv(id))}
}

func taskRecord(id ID, status string, on ...ID) *jv.Object {
	state := obj("status", status)
	if status == StatusWaiting {
		ids := make([]any, len(on))
		for i, o := range on {
			ids[i] = idv(o)
		}
		state.Set("on", ids)
	}
	return obj("id", idv(id), "state", state)
}

func taskWrite(id ID, status string, on ...ID) Change {
	return Change{Kind: ChangeTable, Table: WriteTask, Record: taskRecord(id, status, on...)}
}

// isHit is whether the cache answers.
func isHit(c hopcost.Cache) bool { return c == hopcost.CacheHit }

func newCacheSession() *Session { return New(nil, 1, func() float64 { return 0 }) }

func TestConversationsAreKeptWhenWrittenOrReadAndBounded(t *testing.T) {
	s := newCacheSession()
	if isHit(s.hasConversation(1)) {
		t.Fatal("a conversation nothing wrote or read is a hit")
	}
	s.applyRecords([]Change{conversationWrite(1)})
	if !isHit(s.hasConversation(1)) {
		t.Fatal("a published conversation is a miss")
	}
	s.keepConversation(2)
	if !isHit(s.hasConversation(2)) {
		t.Fatal("a conversation a read found is a miss")
	}
	// The least recently used leaves first, and a hit makes a conversation the most recently used.
	for id := ID(3); id < 3+conversationCapacity-1; id++ {
		s.keepConversation(id)
		if id == 3+conversationCapacity-2 {
			continue
		}
		s.hasConversation(1)
	}
	if !isHit(s.hasConversation(1)) || isHit(s.hasConversation(2)) {
		t.Fatalf("after %d more conversations: 1 = %v (used lately), 2 = %v (the least recently used)", conversationCapacity-1, isHit(s.hasConversation(1)), isHit(s.hasConversation(2)))
	}
}

func TestSubmissionsAreKeptOnlyAsWritten(t *testing.T) {
	s := newCacheSession()
	s.applyRecords([]Change{submissionWrite(7)})
	if !isHit(s.hasSubmission(7)) || isHit(s.hasSubmission(8)) {
		t.Fatalf("written 7 = %v, unwritten 8 = %v", isHit(s.hasSubmission(7)), isHit(s.hasSubmission(8)))
	}
	for id := ID(100); id < 100+submissionCapacity; id++ {
		s.applyRecords([]Change{submissionWrite(id)})
	}
	if isHit(s.hasSubmission(7)) {
		t.Fatal("a submission beyond the bound is still kept")
	}
}

func TestTasksAreLiveUntilTheyEndThenBounded(t *testing.T) {
	s := newCacheSession()
	s.applyRecords([]Change{taskWrite(1, StatusRunning)})
	if !isHit(s.hasTask(1)) {
		t.Fatal("a live task is a miss")
	}
	// Live tasks stay however many ended ones pass.
	for id := ID(10); id < 10+2*endedTaskCapacity; id++ {
		s.applyRecords([]Change{taskWrite(id, StatusRunning), taskWrite(id, StatusTerminal)})
	}
	if !isHit(s.hasTask(1)) {
		t.Fatal("a live task left the cache")
	}
	if isHit(s.hasTask(10)) {
		t.Fatal("an ended task beyond the bound is still kept")
	}
	if !isHit(s.hasTask(10 + 2*endedTaskCapacity - 1)) {
		t.Fatal("the newest ended task is a miss")
	}
	s.applyRecords([]Change{taskWrite(1, StatusTerminal)})
	if !isHit(s.hasTask(1)) {
		t.Fatal("a task that just ended is a miss")
	}
}

func TestAnEndedTaskAWaiterNamesStaysUntilItMovedOn(t *testing.T) {
	s := newCacheSession()
	s.applyRecords([]Change{taskWrite(1, StatusRunning), taskWrite(2, StatusRunning)})
	// Task 2 waits on task 1, which then ends.
	s.applyRecords([]Change{taskWrite(2, StatusWaiting, 1), taskWrite(1, StatusTerminal)})
	for id := ID(10); id < 10+2*endedTaskCapacity; id++ {
		s.applyRecords([]Change{taskWrite(id, StatusTerminal)})
	}
	if !isHit(s.hasTask(1)) {
		t.Fatal("the ended task a waiter names left the cache")
	}
	// The waiter leaves its wait (running again) and changes once more: what it waited on goes back to the bounded list.
	s.applyRecords([]Change{taskWrite(2, StatusRunning)})
	if !isHit(s.hasTask(1)) {
		t.Fatal("the ended task left when the waiter left its wait, before it changed again")
	}
	s.applyRecords([]Change{taskWrite(2, StatusRunning)})
	for id := ID(10000); id < 10000+endedTaskCapacity; id++ {
		s.applyRecords([]Change{taskWrite(id, StatusTerminal)})
	}
	if isHit(s.hasTask(1)) {
		t.Fatal("the ended task stayed after its waiter moved on and the bound passed")
	}
}

func TestAReadOnlySessionKeepsNothing(t *testing.T) {
	s := newCacheSession()
	s.ReadOnly = true
	s.applyRecords([]Change{conversationWrite(1), submissionWrite(1), taskWrite(1, StatusRunning)})
	s.keepConversation(2)
	if isHit(s.hasConversation(1)) || isHit(s.hasConversation(2)) || isHit(s.hasSubmission(1)) || isHit(s.hasTask(1)) {
		t.Fatal("a read-only Session answers a read from its cache")
	}
}

// liveBytes is the heap in use after a full collection.
func liveBytes() uint64 {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// A Session keeps what its bounded caches (cached-storage.ts capacities) and tails hold and nothing per turn beyond them: a turn
// writes a conversation, an entry, a task that ends and a submission, and once the caches are full the live set stays where it was.
func TestTheLiveSetIsBoundedOnceTheCachesAreFull(t *testing.T) {
	s := newCacheSession()
	next := ID(2)
	turn := func() {
		conversation, task := next, next+2
		s.tails.apply([]Change{conversationWrite(conversation), {Kind: ChangeTable, Table: WriteEntry, Record: obj("id", idv(next+1), "conversationId", idv(conversation))}})
		s.applyRecords([]Change{conversationWrite(conversation), taskWrite(task, StatusRunning), taskWrite(task, StatusTerminal), submissionWrite(next + 3)})
		next += 4
	}
	for range 2 * conversationCapacity {
		turn()
	}
	before := liveBytes()
	for range 2 * conversationCapacity {
		turn()
	}
	after := liveBytes()
	if after > before+32<<10 {
		t.Fatalf("%d more turns grew the live set from %d to %d bytes after the caches were full", 2*conversationCapacity, before, after)
	}
	c := &s.records
	if len(c.conversations.nodes) != conversationCapacity || len(c.ended.nodes) != endedTaskCapacity || len(c.submissions.nodes) != submissionCapacity || len(c.live) != 0 || len(c.awaited) != 0 || len(c.waits) != 0 {
		t.Fatalf("conversations %d, ended %d, submissions %d, live %d, awaited %d, waits %d", len(c.conversations.nodes), len(c.ended.nodes), len(c.submissions.nodes), len(c.live), len(c.awaited), len(c.waits))
	}
	if len(s.tails.latest.nodes) != latestCapacity || len(s.tails.markers.nodes) > markerCapacity {
		t.Fatalf("tails: latest %d, markers %d", len(s.tails.latest.nodes), len(s.tails.markers.nodes))
	}
}
