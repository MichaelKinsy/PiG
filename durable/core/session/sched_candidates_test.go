// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import (
	"slices"
	"testing"
)

// Ports packages/durable/src/harness/scheduler.ts #planCandidates (Pi v1.5.2, lines 2005-2028; Pi #11892, commit e50dc7af9) and the tests
// packages/durable/test/harness-tasks.test.ts "runs a waiter whose last dependency is published while a reservation pass plans" (line 790 on).
// A reservation pass takes the candidates out of runnable while it plans: a task made a candidate meanwhile lands in the fresh set, and the
// pass's decision to drop candidates cannot erase it. The candidates it did not drop go back in front of the fresh set, unless they ended or
// hold (completing) meanwhile. Each step is deterministic: the scheduler's sets are driven directly, with no timer or Storage.

func candidateScheduler(status map[ID]string, runnable ...ID) *Scheduler {
	s := &Scheduler{runnable: newOrderedSet[ID](), live: newTaskMap()}
	for id, st := range status {
		s.live.set(id, obj("id", float64(id), "state", obj("status", st)))
	}
	for _, id := range runnable {
		s.runnable.Add(id)
	}
	return s
}

func runnableIDs(s *Scheduler) []ID { return s.runnable.Values() }

// A task made a candidate while the pass plans survives the pass dropping the candidates it decided to drop.
func TestAPassKeepsTheCandidateMadeWhilePlanning(t *testing.T) {
	s := candidateScheduler(map[ID]string{1: StatusRunning, 2: StatusWaiting, 3: StatusRunning, 4: StatusWaiting}, 1, 2, 3)
	s.takeCandidates()
	if got := runnableIDs(s); len(got) != 0 {
		t.Fatalf("runnable while the pass plans = %v, want the fresh empty set", got)
	}
	// The waiter 4 is published runnable while the pass waits; the pass decides to drop 2 (waiting) and keeps 1 and 3.
	s.runnable.Add(4)
	s.candidates.Delete(2)
	s.returnCandidates()
	if got, want := runnableIDs(s), []ID{1, 3, 4}; !slices.Equal(got, want) {
		t.Fatalf("runnable after the pass = %v, want %v: the candidates not dropped, then the task made a candidate meanwhile", got, want)
	}
}

// A candidate that ended, or holds its outcome (completing), while the pass planned does not go back.
func TestAPassDoesNotReturnACandidateThatEndedOrHoldsMeanwhile(t *testing.T) {
	s := candidateScheduler(map[ID]string{1: StatusRunning, 2: StatusRunning, 3: StatusRunning}, 1, 2, 3)
	s.takeCandidates()
	s.live.set(2, obj("id", float64(2), "state", obj("status", StatusCompleting)))
	s.live.delete(3)
	s.returnCandidates()
	if got, want := runnableIDs(s), []ID{1}; !slices.Equal(got, want) {
		t.Fatalf("runnable after the pass = %v, want %v", got, want)
	}
}

// A task that is both a candidate and made a candidate again meanwhile is listed once, in the candidates' place.
func TestAPassListsATaskMadeACandidateAgainOnce(t *testing.T) {
	s := candidateScheduler(map[ID]string{1: StatusRunning, 2: StatusRunning}, 1, 2)
	s.takeCandidates()
	s.runnable.Add(1)
	s.returnCandidates()
	if got, want := runnableIDs(s), []ID{1, 2}; !slices.Equal(got, want) {
		t.Fatalf("runnable after the pass = %v, want %v", got, want)
	}
}

// A region that waited runs the job again from its start: the second run keeps the candidates the first took, so what was made a candidate
// in between is not planned by this pass (it is the next pass's).
func TestAJobRunAgainKeepsTheCandidatesItTook(t *testing.T) {
	s := candidateScheduler(map[ID]string{1: StatusRunning, 2: StatusRunning}, 1)
	s.takeCandidates()
	s.runnable.Add(2)
	s.takeCandidates()
	if got, want := s.candidates.Values(), []ID{1}; !slices.Equal(got, want) {
		t.Fatalf("candidates of the run again = %v, want %v", got, want)
	}
	s.returnCandidates()
	s.returnCandidates() // the job's end returns them again after an error: nothing changes
	if got, want := runnableIDs(s), []ID{1, 2}; !slices.Equal(got, want) {
		t.Fatalf("runnable after the pass = %v, want %v", got, want)
	}
}
