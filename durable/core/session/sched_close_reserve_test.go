// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// gateOnRunning makes the reservation commit of one task wait for the host's Storage once: the line job returns a *NeedGate and
// runs again from its start after the answer (handler.gateCommit).
type gateOnRunning struct {
	*sessiontest.MemStore
	asked bool
}

func (g *gateOnRunning) Commit(writes []session.Write, nextID session.ID) (session.Seq, error) {
	for _, w := range writes {
		if w.Kind == session.WriteTask && !g.asked && taskStatusOf(w.Record) == session.StatusRunning {
			g.asked = true
			return 0, &session.NeedGate{}
		}
	}
	return g.MemStore.Commit(writes, nextID)
}

// Ports packages/durable/test/harness-tasks.test.ts "joins a reservation that settles while closing without starting its handler".
// The reservation pass decides once, at its start, as Pi's callback does: a Seal that arrives while the host's Storage holds the
// commit does not make the job's second run stage nothing, so the reservation lands, no handler starts, and close writes the task
// back as pending.
func TestAGatedReservationCommitLandsWhenCloseSealsWhileTheStorageHoldsIt(t *testing.T) {
	mem := sessiontest.New()
	gated := &gateOnRunning{MemStore: mem}
	s := newSession(gated, mem.NextID())
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
	loop := &session.Loop{}
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	sched.Resume()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if !gated.asked || !loop.Busy() {
		t.Fatalf("the reservation commit did not wait for the Storage (asked %v, busy %v)", gated.asked, loop.Busy())
	}
	sched.Seal()
	loop.Ungate()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
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
