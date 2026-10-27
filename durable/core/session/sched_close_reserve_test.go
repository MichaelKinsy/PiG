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
