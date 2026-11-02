// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// Ports packages/durable/test/harness-tasks.test.ts "joins a reservation that settles while closing without starting its handler".
// The reservation pass decides once, at its start, as Pi's callback does: a Seal that arrives while the Storage holds the commit (the
// writer's call is in flight) does not undo the job, so the reservation lands, no handler starts, and close writes the task back as pending.
func TestAReservationCommitLandsWhenCloseSealsWhileTheStorageHoldsIt(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.TaskDef{Name: "t.never", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var id session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		id, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	ran := false
	registry := snap{"t.never": {Def: *def, Phase: func(*session.Runtime, *jv.Object, func(error)) { ran = true }}}
	loop := bound(s)
	var sched *session.Scheduler
	calls := 0
	sealed := false
	loop.StoreTicks = func(batches [][]session.Write) (int, int, int) {
		calls++
		for _, w := range batches[0] {
			if w.Kind == session.WriteTask && taskStatusOf(w.Record) == session.StatusRunning && !sealed {
				// The reservation commit is in flight: close begins.
				sealed = true
				sched.Seal()
			}
		}
		return 0, 0, 4
	}
	sched = session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if !sealed {
		t.Fatalf("the reservation commit was never in flight (%d Storage calls)", calls)
	}
	rec, err := mem.Task(id)
	if err != nil {
		t.Fatal(err)
	}
	if got := taskStatusOf(rec); got != session.StatusRunning {
		t.Fatalf("the reservation landed as %s, want running", got)
	}
	if ran {
		t.Fatal("the handler started while closing")
	}
	if writes := sched.CloseWrites(); len(writes) != 1 || idOf(writes[0]) != id {
		t.Fatalf("close writes %d records, want the reserved task %d written back", len(writes), id)
	}
}

// A host's createTask operation hands the transaction the input and the initial checkpoint it parsed for it: with Owned the record
// keeps those objects, where a Go caller's values are copied (a 20,000-task commit would hold two more objects per task).
func TestACreatedTaskKeepsAnOwnedInputAndCopiesAnotherInput(t *testing.T) {
	for _, owned := range []bool{false, true} {
		mem := sessiontest.New()
		s := newSession(mem, mem.NextID())
		input, checkpoint := obj("name", "a"), obj("phase", "run")
		def := &session.TaskDef{Name: "t.owned", Version: 1, Initial: func(any) any { return checkpoint }}
		var id session.ID
		if _, err := s.Commit(func(tx *session.Tx) error {
			root, err := tx.CreateRootConversation()
			if err != nil {
				return err
			}
			id, err = tx.CreateTask(def, input, session.TaskOptions{ConversationID: idOf(root), Owned: owned})
			return err
		}, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
		rec, err := mem.Task(id)
		if err != nil {
			t.Fatal(err)
		}
		gotInput, _ := rec.Get("input")
		state, _ := rec.Get("state")
		gotCheckpoint, _ := state.(*jv.Object).Get("checkpoint")
		if kept := gotInput == any(input) && gotCheckpoint == any(checkpoint); kept != owned {
			t.Fatalf("owned %v: the record keeps the caller's objects %v", owned, kept)
		}
		if !jv.Equal(gotInput, input) || !jv.Equal(gotCheckpoint, checkpoint) {
			t.Fatalf("owned %v: input %v checkpoint %v, want %v and %v", owned, gotInput, gotCheckpoint, input, checkpoint)
		}
	}
}

// The tasks a commit creates from equal checkpoints share one object, and a checkpoint with its members in another order is another
// value, since the record serializes them as given.
func TestOwnedTasksCreatedTogetherShareEqualCheckpoints(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.TaskDef{Name: "t.share", Version: 1, Initial: func(input any) any { return input }}
	var ids []session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		for _, cp := range []*jv.Object{obj("phase", "run", "n", 1.0), obj("phase", "run", "n", 1.0), obj("n", 1.0, "phase", "run")} {
			id, err := tx.CreateTask(def, cp, session.TaskOptions{ConversationID: idOf(root), Owned: true})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	checkpointOf := func(id session.ID) any {
		rec, err := mem.Task(id)
		if err != nil {
			t.Fatal(err)
		}
		state, _ := rec.Get("state")
		cp, _ := state.(*jv.Object).Get("checkpoint")
		return cp
	}
	if checkpointOf(ids[0]) != checkpointOf(ids[1]) {
		t.Fatal("equal checkpoints are two objects")
	}
	if checkpointOf(ids[1]) == checkpointOf(ids[2]) {
		t.Fatal("a checkpoint with its members in another order shares the object of one in the first order")
	}
}

// The reservation job that waits for a commit in flight runs again from its start once that commit is published, and Pi's callback does not:
// it decided at its start (scheduler.ts #reserve). A Seal that arrives between the two runs (close begins while the Storage holds a commit
// the job's decision read) does not make the second run reserve nothing, so the reservation lands, no handler starts, and close writes the
// task back as pending (the bug of the gated reservation, 432775653a, with the writer's wait for a publication as the rerun).
func TestAReservationJobThatRunsAgainAfterACommitKeepsTheDecisionOfItsFirstRun(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.TaskDef{Name: "t.never", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var id session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		id, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	ran := false
	registry := snap{"t.never": {Def: *def, Phase: func(*session.Runtime, *jv.Object, func(error)) { ran = true }}}
	loop := bound(s)
	var sched *session.Scheduler
	passes, held := 0, 0
	sealed, armed := false, false
	env := session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }}
	// The first run of the reservation job begins: close begins with it.
	env.ReadTicks = func() int {
		if !armed {
			return 0
		}
		passes++
		if !sealed {
			sealed = true
			sched.Seal()
		}
		return 0
	}
	sched = session.NewScheduler(s, loop, env)
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	armed = true
	// A commit that writes the task is in flight when the reservation job runs: the job waits for its publication and runs again.
	loop.StoreTicks = func(batches [][]session.Write) (int, int, int) {
		for _, w := range batches[0] {
			if w.Kind == session.WriteTask && held == 0 {
				held++
				sched.Resume()
			}
		}
		return 0, 0, 6
	}
	loop.Line(func() error {
		rec, err := mem.Task(id)
		if err != nil {
			return err
		}
		changed := rec.Shallow()
		changed.Set("note", "c0")
		_, err = s.Commit(func(tx *session.Tx) error { return tx.SetTask(changed) }, session.TxScope{})
		return err
	}, func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if held != 1 || passes < 1 {
		t.Fatalf("the reservation job did not run while the commit was in flight (commits held %d, passes %d)", held, passes)
	}
	rec, err := mem.Task(id)
	if err != nil {
		t.Fatal(err)
	}
	if got := taskStatusOf(rec); got != session.StatusRunning {
		t.Fatalf("the reservation landed as %s, want running (passes %d)", got, passes)
	}
	if ran {
		t.Fatal("the handler started while closing")
	}
}
