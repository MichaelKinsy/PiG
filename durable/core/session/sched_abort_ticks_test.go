package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// Pi scheduler.ts #observe signals the run of a task whose abort mark was just published with `queueMicrotask(() =>
// invocation.controller.abort())`, queued inside the publication: the signal fires one microtask pass after the publication, whether or not the
// host has commit listeners (their dispatch is queued first and runs in the same pass, ahead of it). Measured on the pinned Pi (abortTask of a
// running custom task): publication at tick 34, the task's abort listener at 35, with a host observeCommits() listener and without one.
func TestAbortMarkSignalsTheRunOneTickAfterItsPublication(t *testing.T) {
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	def := &session.TaskDef{Name: "t.hold", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	var task session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		task, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	var rt *session.Runtime
	registry := snap{"t.hold": {Def: *def, Phase: func(r *session.Runtime, _ *jv.Object, _ func(error)) { rt = r }}}
	loop := bound(s)
	clock := newHopClock(loop)
	sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: func(error, *jv.Object) {}, Now: func() float64 { return 1 }})
	sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	sched.Resume()
	clock.run(t)
	if rt == nil {
		t.Fatal("the run never started")
	}
	publishedAt, signalledAt := -1, -1
	s.SubscribeCommits(func(*session.Publication) {
		if publishedAt < 0 {
			publishedAt = clock.now
		}
	})
	rt.Signal().OnAbort(func() { signalledAt = clock.now })
	sched.Abort(task, false, nil, func(string, error) {})
	clock.run(t)
	if publishedAt < 0 || signalledAt < 0 {
		t.Fatalf("the abort mark published at %d, the run signalled at %d", publishedAt, signalledAt)
	}
	if signalledAt != publishedAt+1 {
		t.Fatalf("the run was signalled at tick %d, want one after the publication at %d", signalledAt, publishedAt)
	}
}
