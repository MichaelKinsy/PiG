package session_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// The Loop makes the origin a microtask or a line job was queued in current again when it runs it (ABI section 5.4), as a promise
// reaction runs in the async context that registered it.
func TestLoopRunsEachItemInTheOriginItWasQueuedIn(t *testing.T) {
	loop := &session.Loop{}
	var held []uint32
	loop.Retain = func(o uint32) { held = append(held, o) }
	released := 0
	loop.Release = func(uint32) { released++ }
	var seen []uint32
	note := func() { seen = append(seen, loop.Origin) }
	loop.Origin = 3
	loop.Defer(func() { note(); loop.Origin = 99; loop.Defer(note) }) // an item queues in the origin that is current while it runs
	loop.Origin = 4
	loop.Line(func() error { note(); return nil }, func(error) { note() })
	loop.Origin = 0
	loop.Defer(note)
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	// FIFO: the item of 3, the line job's start (queued by Line), the item of none, the item of 99 queued by the first item, then the
	// job's done as a microtask of the job's own origin.
	if want := []uint32{3, 4, 0, 99, 4}; !slices.Equal(seen, want) {
		t.Fatalf("origins seen = %v, want %v", seen, want)
	}
	if !slices.Equal(held, []uint32{4}) || released != 1 {
		t.Fatalf("the line held origins %v and released %d, want 4 held once and released once", held, released)
	}
}

// A kick that finds the scheduler idle queues its drain in the kicker's origin (scheduler.ts #kick queues it with queueMicrotask,
// which captures the kicker's context), so the task it starts runs in it. A kick that finds the scheduler draining queues nothing:
// the pass that is queued keeps the context of the kick that queued it, and starts what the later kick made runnable
// (scheduler.ts #drain loops while dirty).
func TestKickQueuesTheDrainInTheKickersOrigin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kicks []uint32 // the origin of each commit that makes a task runnable, before the loop runs
		want  []uint32 // the origin each task's phase ran in
	}{
		{"an idle scheduler takes the kicker's", []uint32{7}, []uint32{7}},
		{"a draining scheduler keeps its own", []uint32{7, 9}, []uint32{7, 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := sessiontest.New()
			s := newSession(mem, mem.NextID())
			def := &session.TaskDef{Name: "t.node", Version: 1, Initial: func(any) any { return jv.NewObject() }}
			var root session.ID
			if _, err := s.Commit(func(tx *session.Tx) error {
				r, err := tx.CreateRootConversation()
				root = idOf(r)
				return err
			}, session.TxScope{}); err != nil {
				t.Fatal(err)
			}
			loop := &session.Loop{}
			var ran []uint32
			registry := snap{"t.node": {Def: *def, Phase: func(*session.Runtime, *jv.Object, func(error)) { ran = append(ran, loop.Origin) }}}
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
			for _, origin := range tc.kicks {
				loop.Origin = origin
				if _, err := s.Commit(func(tx *session.Tx) error {
					_, err := tx.CreateTask(def, nil, session.TaskOptions{ConversationID: root})
					return err
				}, session.TxScope{}); err != nil {
					t.Fatal(err)
				}
			}
			loop.Origin = 0 // the code that runs the loop is in no origin of its own
			if err := loop.Run(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ran, tc.want) {
				t.Fatalf("the tasks ran in origins %v, want %v", ran, tc.want)
			}
		})
	}
}
